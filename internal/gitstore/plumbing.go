package gitstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// IndexEntry is one blob destined for a tree.
type IndexEntry struct {
	Mode uint32
	OID  string
	Path string
}

// Signature is the commit identity.
type Signature struct {
	Name  string
	Email string
}

// BuildSubtree writes the entries into a temporary index and returns the tree
// OID. The index is rebuilt from scratch (read-tree --empty) so file/directory
// transitions can never silently keep stale entries.
func (s *Store) BuildSubtree(ctx context.Context, entries []IndexEntry, tmpIndex string, allowMissing bool) (string, error) {
	if tmpIndex == "" {
		return "", fmt.Errorf("BuildSubtree requires a temporary index path")
	}
	_ = os.Remove(tmpIndex)
	env := []string{"GIT_INDEX_FILE=" + tmpIndex}

	if _, err := s.runWith(ctx, env, "", "read-tree", "--empty"); err != nil {
		return "", err
	}
	var input bytes.Buffer
	for _, e := range entries {
		mode := strconv.FormatUint(uint64(e.Mode), 8)
		fmt.Fprintf(&input, "%s %s\t%s\x00", mode, e.OID, e.Path)
	}
	if _, err := s.runWith(ctx, env, input.String(), "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	args := []string{"write-tree"}
	if allowMissing {
		args = append(args, "--missing-ok")
	}
	out, err := s.runWith(ctx, env, "", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// SpliceRoot replaces (or removes) exactly the <pid> entry of commit's root
// tree, preserving every other entry byte-for-byte.
func (s *Store) SpliceRoot(ctx context.Context, commit, pid, subtree string, includeSubtree, allowMissing bool) (string, error) {
	var entries bytes.Buffer
	if commit != "" {
		out, err := s.Run(ctx, "ls-tree", "-z", commit)
		if err != nil {
			return "", err
		}
		for _, rec := range strings.Split(out, "\x00") {
			if rec == "" {
				continue
			}
			tab := strings.IndexByte(rec, '\t')
			if tab < 0 {
				return "", fmt.Errorf("malformed ls-tree record %q", rec)
			}
			if rec[tab+1:] == pid {
				continue
			}
			entries.WriteString(rec)
			entries.WriteByte(0)
		}
	}
	if includeSubtree {
		fmt.Fprintf(&entries, "040000 tree %s\t%s\x00", subtree, pid)
	}

	args := []string{"mktree", "-z"}
	if allowMissing {
		args = append(args, "--missing")
	}
	out, err := s.runWith(ctx, nil, entries.String(), args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CommitTree creates a commit object. message goes through stdin, so nothing is
// re-quoted by a shell.
func (s *Store) CommitTree(ctx context.Context, tree, parent, message string, sig Signature) (string, error) {
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-F", "-")

	env := []string{
		"GIT_AUTHOR_NAME=" + sig.Name,
		"GIT_AUTHOR_EMAIL=" + sig.Email,
		"GIT_COMMITTER_NAME=" + sig.Name,
		"GIT_COMMITTER_EMAIL=" + sig.Email,
	}
	out, err := s.runWith(ctx, env, message, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// UpdateRef sets ref to newOID. A non-empty oldOID makes it a compare-and-swap.
func (s *Store) UpdateRef(ctx context.Context, ref, newOID, oldOID string) error {
	args := []string{"update-ref", ref, newOID}
	if oldOID != "" {
		args = append(args, oldOID)
	}
	_, err := s.Run(ctx, args...)
	return err
}

// DeleteRef removes ref; a missing ref is not an error.
func (s *Store) DeleteRef(ctx context.Context, ref string) error {
	_, err := s.Run(ctx, "update-ref", "-d", ref)
	if err != nil {
		var ge *Error
		if asGitError(err, &ge) && ge.Code == 1 {
			return nil
		}
	}
	return err
}

// TreeID resolves <rev>^{tree}.
func (s *Store) TreeID(ctx context.Context, rev string) (string, error) {
	out, err := s.Run(ctx, "rev-parse", rev+"^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Ancestor reports whether a is an ancestor of b (or equal).
func (s *Store) Ancestor(ctx context.Context, a, b string) (bool, error) {
	cmd := s.Runner.Cmd(ctx, "--git-dir="+s.Dir, "merge-base", "--is-ancestor", a, b)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, &Error{
		Args:   []string{"merge-base", "--is-ancestor", a, b},
		Code:   -1,
		Stderr: stderr.String(),
		Kind:   Classify(stderr.String(), -1),
		Err:    err,
	}
}

// Push publishes oid to refs/heads/<branch> with a lease expecting expect
// (empty means the branch must not exist).
func (s *Store) Push(ctx context.Context, url, branch, oid, expect string) error {
	ref := "refs/heads/" + branch
	_, err := s.Run(ctx, "push", "--porcelain",
		"--force-with-lease="+ref+":"+expect,
		url, oid+":"+ref)
	return err
}

// runWith executes a store command with extra environment and optional stdin.
func (s *Store) runWith(ctx context.Context, extraEnv []string, stdin string, args ...string) (string, error) {
	cmd := s.Runner.Cmd(ctx, append([]string{"--git-dir=" + s.Dir}, args...)...)
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Env, extraEnv...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return stdout.String(), ctx.Err()
		}
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return stdout.String(), &Error{
			Args:   args,
			Code:   code,
			Stderr: stderr.String(),
			Kind:   Classify(stderr.String(), code),
			Err:    err,
		}
	}
	return stdout.String(), nil
}
