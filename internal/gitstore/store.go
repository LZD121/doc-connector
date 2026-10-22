package gitstore

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// DetectBranch asks the remote which branch it advertises. It runs exactly one
// `ls-remote --symref` and is the only place doc-connector guesses a branch.
//
// empty is true when the remote has no refs (ls-remote exits 2 under
// --exit-code). A network or authentication failure is returned as *Error.
func DetectBranch(ctx context.Context, r *Runner, url string) (branch string, empty bool, err error) {
	out, err := r.Run(ctx, "ls-remote", "--symref", "--exit-code", url, "HEAD")
	if err != nil {
		var ge *Error
		if ok := asGitError(err, &ge); ok && ge.Code == 2 && strings.TrimSpace(ge.Stderr) == "" {
			return "", true, nil
		}
		return "", false, err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ref:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "HEAD" {
			continue
		}
		ref := fields[0]
		const prefix = "refs/heads/"
		if strings.HasPrefix(ref, prefix) && len(ref) > len(prefix) {
			return strings.TrimPrefix(ref, prefix), false, nil
		}
	}
	return "", false, fmt.Errorf("remote %s did not advertise a HEAD branch", url)
}

// Store is a bare mirror of the private remote, addressed by absolute
// --git-dir. It never has a work tree.
type Store struct {
	Dir    string
	Runner *Runner
}

// NewStore returns a store rooted at dir.
func NewStore(r *Runner, dir string) *Store {
	return &Store{Dir: dir, Runner: r}
}

// Run executes git against the store with an absolute --git-dir.
func (s *Store) Run(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"--git-dir=" + s.Dir}, args...)
	return s.Runner.Run(ctx, full...)
}

// EnsureBare creates the bare repository if it does not exist yet. Repeated
// calls leave an existing mirror untouched.
func (s *Store) EnsureBare(ctx context.Context, branch string) error {
	if _, err := s.Run(ctx, "rev-parse", "--git-dir"); err == nil {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	_, err := s.Runner.Run(ctx, "init", "--bare", "-b", branch, s.Dir)
	return err
}

// Fetch retrieves all heads into refs/remotes/origin/* with a glob refspec,
// which also succeeds against an empty remote (an explicit refspec fatals).
func (s *Store) Fetch(ctx context.Context, url string) error {
	_, err := s.Run(ctx, "fetch", "--no-tags", "--prune", url, "+refs/heads/*:refs/remotes/origin/*")
	return err
}

// RefTip resolves a ref to an object id; missing refs return ("", nil).
func (s *Store) RefTip(ctx context.Context, ref string) (string, error) {
	out, err := s.Run(ctx, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		var ge *Error
		if asGitError(err, &ge) && ge.Code == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// TreeOf returns the tree object id of <commit>:<pid>, or ("", false, nil) when
// the prefix does not exist.
func (s *Store) TreeOf(ctx context.Context, commit, pid string) (string, bool, error) {
	if commit == "" {
		return "", false, nil
	}
	out, err := s.Run(ctx, "rev-parse", "--verify", "--quiet", commit+":"+pid)
	if err != nil {
		var ge *Error
		if asGitError(err, &ge) && ge.Code == 1 {
			return "", false, nil
		}
		return "", false, err
	}
	oid := strings.TrimSpace(out)
	if oid == "" {
		return "", false, nil
	}
	return oid, true, nil
}

// RemoteBranch returns refs/remotes/origin/<branch>.
func RemoteBranch(branch string) string { return "refs/remotes/origin/" + branch }

// asGitError is a tiny errors.As helper kept local to avoid importing errors in
// every call site.
func asGitError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
