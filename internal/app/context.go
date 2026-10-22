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
	"sort"
	"strings"
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

// docsEntry is one mapped docs directory in the loaded project.
//
// Budget is the number of characters left for a path inside Abs after the
// directory path and the remote prefix, so a deeply nested docs directory gets
// a proportionally smaller allowance.
type docsEntry struct {
	Mapping config.DocsMapping
	Abs     string
	Root    *fsx.Root
	Budget  int
}

// RepoContext is the loaded state of one initialized project.
//
// Docs holds every mapped docs directory in configuration order. DocsDir and
// Root mirror Docs[0] so single-directory code paths read the same as before
// multi-directory support existed.
type RepoContext struct {
	Repo       *project.Repo
	Runner     *gitstore.Runner
	Work       *gitstore.Runner
	Store      *gitstore.Store
	Config     *config.Config
	State      state.State
	Exclude    string
	Docs       []docsEntry
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
	relDirs := make([]string, 0, len(cfg.DocsDirs))
	for _, m := range cfg.DocsDirs {
		relDirs = append(relDirs, m.Path)
	}
	if err := project.GuardAllDirs(ctx, work, relDirs, exclude); err != nil {
		return nil, wrapErr(err, codeGuard)
	}

	rc := &RepoContext{
		Repo:       repo,
		Runner:     r,
		Work:       work,
		Store:      gitstore.NewStore(work, filepath.Join(config.Dir(repo.GitDir), "store.git")),
		Config:     cfg,
		State:      st,
		Exclude:    exclude,
		warnWriter: env.Stderr,
	}
	absDirs := make([]string, 0, len(cfg.DocsDirs))
	for _, m := range cfg.DocsDirs {
		abs := filepath.Join(repo.WorkTree, filepath.FromSlash(m.Path))
		if opts.CreateDocs {
			if err := os.MkdirAll(abs, 0o700); err != nil {
				rc.Close()
				return nil, errf(1, "cannot create docs directory %q: %v", abs, err).
					withHint("check the parent directory permissions and free space, then retry")
			}
		}
		entry := docsEntry{Mapping: m, Abs: abs, Budget: docsBudget(cfg, m)}
		if _, statErr := os.Lstat(abs); statErr == nil {
			root, oerr := fsx.Open(abs)
			if oerr != nil {
				rc.Close()
				return nil, wrapErr(oerr, codeGuard)
			}
			entry.Root = root
		}
		rc.Docs = append(rc.Docs, entry)
		absDirs = append(absDirs, abs)
	}
	rc.DocsDir = rc.Docs[0].Abs
	rc.Root = rc.Docs[0].Root
	rc.Filter = ignore.NewWithDocsIgnore(cfg.Ignore.Extra, absDirs)
	for _, w := range rc.Filter.Warnings() {
		fmt.Fprintf(env.Stderr, "warning: %s\n", w)
	}
	return rc, nil
}

// docsBudget is the character budget for one path inside a mapped docs
// directory: the global ceiling minus the directory path, the remote prefix and
// the separating slash. It never drops below 1, so an over-long configuration
// still validates paths on their own merits.
func docsBudget(cfg *config.Config, m config.DocsMapping) int {
	budget := cfg.Limits.MaxPathChars - len(m.Path) - len(m.Prefix) - 1
	if budget < 1 {
		return 1
	}
	return budget
}

// projectPath builds the path a file has inside the project subtree:
// <prefix>/<rel>, or <rel> for the project root.
func projectPath(prefix, rel string) string {
	switch {
	case prefix == "":
		return rel
	case rel == "":
		return prefix
	default:
		return prefix + "/" + rel
	}
}

// resolveProjectPath maps a project path back to the docs directory that owns
// it and the path inside that directory. The longest matching prefix wins; an
// empty-prefix mapping absorbs every other path, and its name alone (a file
// called like a prefix) never counts as the prefix subtree.
func (rc *RepoContext) resolveProjectPath(p string) (docsEntry, string, error) {
	best := -1
	for i, e := range rc.Docs {
		prefix := e.Mapping.Prefix
		if prefix == "" || p == prefix || !strings.HasPrefix(p, prefix+"/") {
			continue
		}
		if best < 0 || len(prefix) > len(rc.Docs[best].Mapping.Prefix) {
			best = i
		}
	}
	if best >= 0 {
		e := rc.Docs[best]
		return e, strings.TrimPrefix(p, e.Mapping.Prefix+"/"), nil
	}
	for _, e := range rc.Docs {
		if e.Mapping.Prefix == "" {
			return e, p, nil
		}
	}
	return docsEntry{}, "", rc.unmappedError([]string{p})
}

// prefixLabels lists the configured remote prefixes in configuration order; the
// empty prefix reads as the project root so the hint is actionable.
func (rc *RepoContext) prefixLabels() string {
	labels := make([]string, 0, len(rc.Docs))
	for _, e := range rc.Docs {
		if e.Mapping.Prefix == "" {
			labels = append(labels, "(project root)")
			continue
		}
		labels = append(labels, e.Mapping.Prefix)
	}
	return strings.Join(labels, ", ")
}

// unmappedError reports remote paths that no configured mapping owns. It is a
// guard failure: nothing may be written for a path whose home is unknown.
func (rc *RepoContext) unmappedError(paths []string) *Error {
	ordered := append([]string{}, paths...)
	sort.Strings(ordered)
	list := strings.Join(ordered, ", ")
	msg := fmt.Sprintf("remote path %q maps to no configured docs directory", ordered[0])
	if len(ordered) > 1 {
		msg = fmt.Sprintf("%d remote path(s) map to no configured docs directory: %s", len(ordered), list)
	}
	return errf(codeGuard, "%s", msg).
		withHint("configured remote prefixes: %s; add a docs_dirs mapping that covers %s, or remove them from the remote",
			rc.prefixLabels(), list)
}

// resolveUserPath resolves a path typed by the user: a project path, or a bare
// path when exactly one mapped directory could own it.
func (rc *RepoContext) resolveUserPath(p string) (docsEntry, string, error) {
	if e, rel, err := rc.resolveProjectPath(p); err == nil {
		return e, rel, nil
	}
	var entries []docsEntry
	candidates := make([]string, 0, len(rc.Docs))
	for _, e := range rc.Docs {
		if e.Mapping.Prefix == "" {
			continue
		}
		entries = append(entries, e)
		candidates = append(candidates, projectPath(e.Mapping.Prefix, p))
	}
	if len(entries) == 1 {
		return entries[0], p, nil
	}
	sort.Strings(candidates)
	return docsEntry{}, "", errf(codeGuard, "path %q is ambiguous across %d mapped docs directories", p, len(candidates)).
		withHint("pass one of: %s", strings.Join(candidates, ", "))
}

// ensureMapped refuses when a project tree holds a path no configured mapping
// owns. A push in that situation would delete the path from the remote and a
// pull would write it somewhere the user never configured.
func (rc *RepoContext) ensureMapped(tree trees.Tree) error {
	var unmapped []string
	for p := range tree {
		if _, _, err := rc.resolveProjectPath(p); err != nil {
			unmapped = append(unmapped, p)
		}
	}
	if len(unmapped) == 0 {
		return nil
	}
	return rc.unmappedError(unmapped)
}

// removeConflictArtifact deletes a conflict artifact recorded as a project path.
// A missing docs directory means there is nothing left to remove.
func removeConflictArtifact(rc *RepoContext, theirs string) error {
	e, rel, err := rc.resolveProjectPath(theirs)
	if err != nil {
		return err
	}
	if e.Root == nil {
		return nil
	}
	return e.Root.Remove(rel)
}

// Close releases every docs root.
func (rc *RepoContext) Close() {
	for _, e := range rc.Docs {
		if e.Root != nil {
			_ = e.Root.Close()
		}
	}
}

// ScanPolicy carries the size tiers and link handling for one scan.
type ScanPolicy struct {
	WarnBytes      int64
	MaxBytes       int64
	Force          bool
	FollowSymlinks bool
}

// ScanLocal hashes every docs directory without writing blobs. Tree keys are
// project paths, so one tree covers every mapping.
func (rc *RepoContext) ScanLocal(ctx context.Context, policy ScanPolicy) (*scan.Result, error) {
	return rc.scanAll(ctx, policy, false)
}

// scanAll scans every existing docs directory and merges the results. Each
// directory's tree is re-keyed to its project path; ignored counts and byte
// totals add up, and skipped entries are reported as project paths.
func (rc *RepoContext) scanAll(ctx context.Context, policy ScanPolicy, write bool) (*scan.Result, error) {
	out := &scan.Result{Tree: trees.Tree{}}
	owner := map[string]string{}
	for _, e := range rc.Docs {
		if e.Root == nil {
			continue
		}
		res, err := scan.Scan(ctx, rc.Store, e.Abs, scan.Options{
			Write:          write,
			Filter:         rc.Filter,
			MaxBytes:       policy.MaxBytes,
			WarnBytes:      policy.WarnBytes,
			Force:          policy.Force,
			FollowSymlinks: policy.FollowSymlinks,
		})
		if err != nil {
			return nil, wrapErr(err, 1)
		}
		out.Ignored += res.Ignored
		out.TotalBytes += res.TotalBytes
		for _, skipped := range res.Skipped {
			out.Skipped = append(out.Skipped, projectPath(e.Mapping.Prefix, skipped))
		}
		for rel, entry := range res.Tree {
			key := projectPath(e.Mapping.Prefix, rel)
			if prev, ok := owner[key]; ok {
				return nil, errf(codeGuard, "docs path %q is produced by both %q and %q", key, prev, e.Mapping.Path).
					withHint("give one of those docs_dirs a distinct prefix so every file maps to exactly one remote path")
			}
			owner[key] = e.Mapping.Path
			out.Tree[key] = entry
		}
	}
	sort.Strings(out.Skipped)
	keys := make([]string, 0, len(out.Tree))
	for k := range out.Tree {
		keys = append(keys, k)
	}
	if err := fsx.CheckCaseCollisions(keys); err != nil {
		return nil, wrapErr(err, codeGuard)
	}
	return out, nil
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

// FetchedAgeSeconds is the age of the fetch cache in whole seconds, or 0 when
// there is no usable timestamp. It is the machine-readable form of FetchedAge.
func FetchedAgeSeconds(f state.Fetched) int64 {
	if f.At == "" {
		return 0
	}
	at, err := time.Parse(time.RFC3339, f.At)
	if err != nil {
		return 0
	}
	d := time.Since(at)
	if d < 0 {
		return 0
	}
	return int64(d.Seconds())
}

// FetchedAge renders the cache age, or "" when unknown.
func FetchedAge(f state.Fetched) string {
	if f.At == "" {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, f.At); err != nil {
		return ""
	}
	d := time.Duration(FetchedAgeSeconds(f)) * time.Second
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

// validatePath applies the shared sync-path rules.
func validatePath(rel string, budget int) error {
	return fsx.ValidateSyncPath(rel, budget)
}
