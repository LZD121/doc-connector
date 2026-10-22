// Package project locates the team repository and derives the deterministic
// project identity that names this project's subtree in the private remote.
package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/LZD121/doc-connector/internal/gitstore"
)

// Error is a domain error carrying the exit code fixed by REQUIREMENTS CLI-02.
type Error struct {
	Code int
	Msg  string
	Hint string
	Err  error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

// ExitCode lets cli.Wrap preserve the domain code.
func (e *Error) ExitCode() int { return e.Code }

// HintText returns an actionable next step.
func (e *Error) HintText() string { return e.Hint }

func guardErr(format string, args ...any) *Error {
	return &Error{Code: 3, Msg: fmt.Sprintf(format, args...)}
}

func usageErr(format string, args ...any) *Error {
	return &Error{Code: 2, Msg: fmt.Sprintf(format, args...)}
}

// Repo describes the team repository enclosing the working directory.
type Repo struct {
	WorkTree     string // absolute worktree root
	GitDir       string // absolute git dir (may be .git/worktrees/<name>)
	CommonDir    string // absolute common git dir
	ObjectFormat string // "sha1" or "sha256"
	Shallow      bool
	Bare         bool
}

// Discover resolves repository paths for dir. A non-repository is exit 2, a
// bare repository or linked worktree is exit 3 (the hidden state would be
// ambiguous or shared).
func Discover(ctx context.Context, r *gitstore.Runner, dir string) (*Repo, error) {
	local := *r
	local.Dir = dir

	// Detect a bare repository before asking for a work tree: --show-toplevel
	// fails there, and the actionable answer is "bare repo", not "not a repo".
	bareOut, err := local.Run(ctx, "rev-parse", "--is-bare-repository")
	if err != nil {
		return nil, usageErr("not inside a git repository: %s", strings.TrimSpace(dir)).
			withHint("run this command inside the repository that owns your documentation")
	}
	if strings.TrimSpace(bareOut) == "true" {
		gitDirOut, _ := local.Run(ctx, "rev-parse", "--absolute-git-dir")
		return nil, guardErr("refusing to run in a bare repository: %s", strings.TrimSpace(gitDirOut)).
			withHint("run doc-connector inside a normal working tree")
	}

	toplevel, err := local.Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, usageErr("not inside a git work tree: %s", strings.TrimSpace(dir)).
			withHint("run this command inside the repository that owns your documentation")
	}
	workTree := strings.TrimSpace(toplevel)

	r2 := local
	r2.Dir = workTree

	gitDirOut, err := r2.Run(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	gitDir, err := filepath.Abs(strings.TrimSpace(gitDirOut))
	if err != nil {
		return nil, usageErr("cannot resolve git dir: %v", err)
	}

	commonOut, err := r2.Run(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	common := strings.TrimSpace(commonOut)
	if !filepath.IsAbs(common) {
		common = filepath.Join(workTree, common)
	}
	commonDir, err := filepath.Abs(common)
	if err != nil {
		return nil, usageErr("cannot resolve common git dir: %v", err)
	}

	shallowOut, err := r2.Run(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return nil, err
	}
	shallow := strings.TrimSpace(shallowOut) == "true"

	format := "sha1"
	if out, err := r2.Run(ctx, "rev-parse", "--show-object-format"); err == nil {
		if v := strings.TrimSpace(out); v != "" {
			format = v
		}
	}

	if !samePath(gitDir, commonDir) {
		return nil, guardErr("refusing to run in a linked worktree: git dir %s differs from common dir %s", gitDir, commonDir).
			withHint("linked worktrees share hidden state; run doc-connector from the main worktree")
	}

	return &Repo{
		WorkTree:     workTree,
		GitDir:       gitDir,
		CommonDir:    commonDir,
		ObjectFormat: format,
		Shallow:      shallow,
	}, nil
}

// samePath compares two paths as the filesystem sees them. git may report the
// worktree through a symlinked prefix (/var vs /private/var on macOS) while the
// git dir is canonical, and Windows may mix 8.3 short names with long ones.
func samePath(a, b string) bool {
	if ai, err := os.Stat(a); err == nil {
		if bi, err := os.Stat(b); err == nil {
			return os.SameFile(ai, bi)
		}
	}
	ca, cb := canonicalPath(a), canonicalPath(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

func (e *Error) withHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}
