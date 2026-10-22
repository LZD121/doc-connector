package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LZD121/doc-connector/internal/gitstore"
)

// GuardUntracked refuses when any path under docsDir is tracked by the team
// repository. This check must run before writing the exclude block: once a path
// is tracked, an exclude entry is meaningless and committing the private docs
// may already be one `git add -A` away.
func GuardUntracked(ctx context.Context, r *gitstore.Runner, docsDir string) error {
	out, err := r.Run(ctx, "ls-files", "-z", "--", docsDir)
	if err != nil {
		return err
	}
	paths := splitNUL(out)
	if len(paths) == 0 {
		return nil
	}
	shown := paths
	suffix := ""
	if len(shown) > 5 {
		shown = shown[:5]
		suffix = fmt.Sprintf(" (and %d more)", len(paths)-5)
	}
	return guardErr("docs directory %q is tracked by the team repository: %s%s",
		docsDir, strings.Join(shown, ", "), suffix).
		withHint("run \"git rm --cached -r %s\" yourself after deciding how to handle those files; doc-connector will not touch the index", docsDir)
}

// GuardIgnored requires docsDir to be ignored and requires the matching rule to
// come from exactly the managed info/exclude file. A rule in .gitignore (or a
// negation that lets the directory leak) is refused with exit 3.
func GuardIgnored(ctx context.Context, r *gitstore.Runner, docsDir, excludePath string) error {
	query := strings.TrimSuffix(filepath.ToSlash(docsDir), "/") + "/\x00"
	cmd := r.Cmd(ctx, "check-ignore", "-v", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(query)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return guardErr("docs directory %q is not ignored", docsDir).
				withHint("re-run init to write the managed block into %s", excludePath)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return guardErr("cannot verify ignore rules for %q: %s", docsDir, detail)
	}

	fields := strings.Split(stdout.String(), "\x00")
	if len(fields) < 4 {
		return guardErr("unexpected check-ignore output while verifying %q", docsDir)
	}
	source, line, pattern := fields[0], fields[1], fields[2]
	resolved := source
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(r.Dir, resolved)
	}
	if !samePath(resolved, excludePath) {
		return guardErr("docs directory %q is ignored by %s:%s (%s), not by %s",
			docsDir, source, line, pattern, excludePath).
			withHint("remove that rule from the other ignore file; doc-connector only accepts its own managed block")
	}
	return nil
}

// GuardAll runs the isolation checks in their required order.
func GuardAll(ctx context.Context, r *gitstore.Runner, docsDir, excludePath string) error {
	return GuardAllDirs(ctx, r, []string{docsDir}, excludePath)
}

// GuardAllDirs runs both isolation checks for every mapped docs directory, in
// the same order as GuardAll. The first offending directory fails the call and
// names itself, so a multi-directory project reports the specific one.
func GuardAllDirs(ctx context.Context, r *gitstore.Runner, docsDirs []string, excludePath string) error {
	for _, dir := range docsDirs {
		if err := GuardUntracked(ctx, r, dir); err != nil {
			return err
		}
	}
	for _, dir := range docsDirs {
		if err := GuardIgnored(ctx, r, dir, excludePath); err != nil {
			return err
		}
	}
	return nil
}

func splitNUL(s string) []string {
	parts := strings.Split(s, "\x00")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Snapshot captures the team repository facts that doc-connector must never
// change. It is used by integration tests, not as a runtime assertion: a user
// editing the repository concurrently would otherwise trip a false alarm.
type Snapshot struct {
	IndexHash   string
	HEAD        string
	RefsHash    string
	HooksHash   string
	TrackedHash string
}

// TakeSnapshot hashes index bytes, HEAD, refs, hook files and the tracked set.
func TakeSnapshot(ctx context.Context, r *gitstore.Runner, repo *Repo) (Snapshot, error) {
	var snap Snapshot

	indexPath := filepath.Join(repo.GitDir, "index")
	if data, err := os.ReadFile(indexPath); err == nil {
		snap.IndexHash = hashBytes(data)
	}

	if out, err := r.Run(ctx, "rev-parse", "--verify", "HEAD"); err == nil {
		snap.HEAD = strings.TrimSpace(out)
	}

	if out, err := r.Run(ctx, "for-each-ref", "--format=%(refname) %(objectname)", "refs/"); err == nil {
		snap.RefsHash = hashBytes([]byte(out))
	}

	if out, err := r.Run(ctx, "ls-files", "-z"); err == nil {
		snap.TrackedHash = hashBytes([]byte(out))
	}

	hooks, err := hashDir(filepath.Join(repo.CommonDir, "hooks"))
	if err != nil {
		return Snapshot{}, err
	}
	snap.HooksHash = hashBytes([]byte(hooks))
	return snap, nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashDir(dir string) (string, error) {
	entries := make([]string, 0)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		entries = append(entries, rel+":"+hashBytes(data))
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n"), nil
}
