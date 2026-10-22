// Package app orchestrates commands: guard, scan, classify, apply, report.
// The CLI layer only parses flags and formats results.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/fsx"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/ignore"
	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/scan"
	"github.com/LZD121/doc-connector/internal/state"
	"github.com/LZD121/doc-connector/internal/trees"
)

// Exit codes mirror the CLI contract (REQUIREMENTS CLI-02).
const (
	codeConflict = 1
	codeUsage    = 2
	codeGuard    = 3
	codeRemote   = 4
)

// Env carries process dependencies for command orchestration.
type Env struct {
	Cwd     string
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
}

// Error is an application failure with an exit code.
type Error struct {
	Code int
	Msg  string
	Hint string
	Err  error
}

func (e *Error) Error() string    { return e.Msg }
func (e *Error) Unwrap() error    { return e.Err }
func (e *Error) ExitCode() int    { return e.Code }
func (e *Error) HintText() string { return e.Hint }

func errf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// RepoContext is the loaded state of one initialized project.
type RepoContext struct {
	Repo       *project.Repo
	Runner     *gitstore.Runner
	Work       *gitstore.Runner
	Store      *gitstore.Store
	Config     *config.Config
	State      state.State
	Exclude    string
	DocsDir    string
	Root       *fsx.Root
	Filter     *ignore.Filter
	warnWriter io.Writer
}

// LoadOptions controls docs-directory creation.
type LoadOptions struct {
	// CreateDocs creates an absent docs directory (pull needs it).
	CreateDocs bool
}

// Load resolves the repository, hidden config/state and isolation guards.
func Load(ctx context.Context, env Env, opts LoadOptions) (*RepoContext, error) {
	r := gitstore.New(env.Cwd)
	if err := gitstore.EnsureMinimum(ctx, r); err != nil {
		return nil, wrapErr(err, codeUsage)
	}
	repo, err := project.Discover(ctx, r, env.Cwd)
	if err != nil {
		return nil, wrapErr(err, codeUsage)
	}
	work := gitstore.New(repo.WorkTree)

	cfg, warnings, err := config.Load(repo.GitDir)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, errf(codeUsage, "this repository is not initialized").
				withHint("run \"doc-connector init --remote <url>\" first")
		}
		return nil, wrapErr(err, codeUsage)
	}
	for _, w := range warnings {
		fmt.Fprintf(env.Stderr, "warning: %s\n", w)
	}

	st, err := state.Load(repo.GitDir)
	if err != nil {
		return nil, wrapErr(err, codeUsage)
	}
	if err := st.CheckIdentity(cfg.Project.ID, cfg.Branch); err != nil {
		return nil, wrapErr(err, codeUsage)
	}

	exclude := project.ExcludePath(repo.CommonDir)
	if err := project.GuardUntracked(ctx, work, cfg.DocsDir); err != nil {
		return nil, wrapErr(err, codeGuard)
	}
	if err := project.GuardIgnored(ctx, work, cfg.DocsDir, exclude); err != nil {
		return nil, wrapErr(err, codeGuard)
	}

	docsDir := filepath.Join(repo.WorkTree, filepath.FromSlash(cfg.DocsDir))
	if opts.CreateDocs {
		if err := os.MkdirAll(docsDir, 0o700); err != nil {
			return nil, errf(1, "cannot create docs directory %q: %v", docsDir, err)
		}
	}
	var root *fsx.Root
	if _, statErr := os.Lstat(docsDir); statErr == nil {
		root, err = fsx.Open(docsDir)
		if err != nil {
			return nil, wrapErr(err, codeGuard)
		}
	}

	rc := &RepoContext{
		Repo:       repo,
		Runner:     r,
		Work:       work,
		Store:      gitstore.NewStore(work, filepath.Join(config.Dir(repo.GitDir), "store.git")),
		Config:     cfg,
		State:      st,
		Exclude:    exclude,
		DocsDir:    docsDir,
		Root:       root,
		Filter:     ignore.New(cfg.Ignore.Extra),
		warnWriter: env.Stderr,
	}
	return rc, nil
}

// Close releases the docs root.
func (rc *RepoContext) Close() {
	if rc.Root != nil {
		_ = rc.Root.Close()
	}
}

// ScanLocal hashes the docs directory without writing blobs.
func (rc *RepoContext) ScanLocal(ctx context.Context) (*scan.Result, error) {
	if rc.Root == nil {
		return &scan.Result{Tree: trees.Tree{}}, nil
	}
	res, err := scan.Scan(ctx, rc.Store, rc.DocsDir, scan.Options{
		Filter:   rc.Filter,
		MaxBytes: rc.Config.Limits.MaxBytes,
	})
	if err != nil {
		return nil, wrapErr(err, 1)
	}
	return res, nil
}

// Fetch updates the mirror and the fetched cache, returning the remote tip.
func (rc *RepoContext) Fetch(ctx context.Context, by string) (string, error) {
	if err := rc.Store.Fetch(ctx, rc.Config.Remote); err != nil {
		return "", wrapErr(err, codeRemote)
	}
	tip, err := rc.Store.RefTip(ctx, gitstore.RemoteBranch(rc.Config.Branch))
	if err != nil {
		return "", wrapErr(err, 1)
	}
	if err := state.SaveFetched(rc.Repo.GitDir, state.NewFetched(tip, by)); err != nil {
		fmt.Fprintf(rc.warnWriter, "warning: cannot update fetch cache: %v\n", err)
	}
	return tip, nil
}

// Baseline loads the baseline tree from the synced commit.
func (rc *RepoContext) Baseline(ctx context.Context) (trees.Tree, error) {
	tree, warnings, err := trees.LoadBaseline(ctx, rc.Store, rc.State, rc.Config.Project.ID)
	if err != nil {
		return nil, wrapErr(err, 1)
	}
	for _, w := range warnings {
		fmt.Fprintf(rc.warnWriter, "warning: %s\n", w)
	}
	return tree, nil
}

// RemoteTree loads <tip>:<project_id>; an absent prefix is an empty tree.
func (rc *RepoContext) RemoteTree(ctx context.Context, tip string) (trees.Tree, string, error) {
	if tip == "" {
		return trees.Tree{}, "", nil
	}
	oid, ok, err := rc.Store.TreeOf(ctx, tip, rc.Config.Project.ID)
	if err != nil {
		return nil, "", wrapErr(err, 1)
	}
	if !ok {
		return trees.Tree{}, "", nil
	}
	tree, err := trees.ReadTree(ctx, rc.Store, tip+":"+rc.Config.Project.ID)
	if err != nil {
		return nil, "", wrapErr(err, codeGuard)
	}
	return tree, oid, nil
}

// shortOID returns the first 12 characters.
func shortOID(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}

// FetchedAge renders the cache age, or "" when unknown.
func FetchedAge(f state.Fetched) string {
	if f.At == "" {
		return ""
	}
	at, err := time.Parse(time.RFC3339, f.At)
	if err != nil {
		return ""
	}
	d := time.Since(at)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

func (e *Error) withHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

func wrapErr(err error, fallback int) error {
	if err == nil {
		return nil
	}
	if c, ok := err.(interface {
		error
		ExitCode() int
		HintText() string
	}); ok {
		return &Error{Code: c.ExitCode(), Msg: c.Error(), Hint: c.HintText(), Err: err}
	}
	return &Error{Code: fallback, Msg: err.Error(), Err: err}
}
