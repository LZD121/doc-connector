package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/scan"
	"github.com/LZD121/doc-connector/internal/state"
	"github.com/LZD121/doc-connector/internal/trees"
)

// PushOptions controls one push.
type PushOptions struct {
	DryRun         bool
	Resolve        []string
	AllowLarge     bool
	Force          bool
	FollowSymlinks bool
}

// PushResult reports what push did.
type PushResult struct {
	Added    int
	Modified int
	Deleted  int
	Tracked  int
	Commit   string
	Tree     string
	Parent   string
	DryRun   bool
	NoOp     bool
	Resolved []string
}

// lockWait bounds how long a writer waits for the project lock before it
// reports that another process is working (exit 3). Mirrors the init command.
var lockWait = 10 * time.Second

// acquireProjectLock takes the exclusive project lock for one command. The
// returned release func is safe on nil and must be deferred by the caller so no
// return path leaves the lock held.
func acquireProjectLock(ctx context.Context, rc *RepoContext, cmd string) (func(), error) {
	lockCtx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	lock, err := state.Acquire(lockCtx, rc.Repo.GitDir, cmd, rc.Config.Machine)
	if err != nil {
		return nil, wrapErr(err, codeGuard)
	}
	return func() { _ = lock.Release() }, nil
}

// Push publishes the docs directory as <project_id>/.
func Push(ctx context.Context, env Env, opts PushOptions) (*PushResult, error) {
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: false})
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// Dry-run takes the lock too: it reads the baseline and the remote tip.
	release, err := acquireProjectLock(ctx, rc, "push")
	if err != nil {
		return nil, err
	}
	defer release()
	if err := rc.requireRoots(); err != nil {
		return nil, err
	}
	if err := recoverJournal(ctx, rc); err != nil {
		return nil, err
	}
	if st, lerr := state.Load(rc.Repo.GitDir); lerr == nil {
		rc.State = st
	}

	resolved, resolvedEntries, err := resolveConflicts(rc, opts.Resolve)
	if err != nil {
		return nil, err
	}
	if remaining := len(rc.State.Unresolved) - len(resolved); remaining > 0 {
		paths := make([]string, 0, remaining)
		for _, c := range rc.State.Unresolved {
			if !resolved[c.Path] {
				paths = append(paths, c.Path)
			}
		}
		return nil, errf(codeConflict, "push refused: %d unresolved conflict path(s): %s", len(paths), strings.Join(paths, ", ")).
			withHint("merge the files and run push --resolve <path> for each, or use restore --force in the next phase")
	}

	tip, err := rc.Fetch(ctx, "push")
	if err != nil {
		return nil, err
	}

	maxBytes := rc.Config.Limits.MaxBytes
	if opts.AllowLarge {
		maxBytes = 0
	}
	warnBytes := rc.Config.Limits.WarnBytes
	localRes, err := scanForPush(ctx, rc, !opts.DryRun, maxBytes, warnBytes, opts.Force, opts.FollowSymlinks)
	if err != nil {
		return nil, err
	}
	local := localRes.Tree

	base, err := rc.Baseline(ctx)
	if err != nil {
		return nil, err
	}
	remote, _, err := rc.RemoteTree(ctx, tip)
	if err != nil {
		return nil, err
	}
	// The remote subtree may hold paths from an older mapping. Pushing would
	// delete them, so refuse before anything is built or written.
	if err := rc.ensureMapped(remote); err != nil {
		return nil, err
	}

	if runtime.GOOS == "windows" {
		local = trees.NormalizeModes(local, base, remote)
	}

	if rc.State.SyncedCommit != "" && tip != "" {
		ok, aerr := rc.Store.Ancestor(ctx, rc.State.SyncedCommit, tip)
		if aerr != nil {
			return nil, wrapErr(aerr, 1)
		}
		if !ok {
			return nil, errf(codeConflict, "remote history no longer contains the last synced commit").
				withHint("the private repository was rewritten externally; inspect it before pushing again")
		}
	}

	if rc.Config.Project.Adoption != "local" || rc.State.SyncedCommit != "" {
		if clobber := trees.Clobber(base, local, remote, resolved); len(clobber) > 0 {
			return nil, errf(codeConflict, "push refused: %d remote path(s) have versions this machine has not seen: %s",
				len(clobber), strings.Join(clobber, ", ")).
				withHint("run \"doc-connector pull\" first; push never overwrites an unseen remote version")
		}
	}

	res := &PushResult{DryRun: opts.DryRun, Resolved: keys(resolved)}
	res.Added, res.Modified, res.Deleted = diffCounts(local, remote)
	res.Tracked = len(local)

	entries := make([]gitstore.IndexEntry, 0, len(local))
	for p, e := range local {
		entries = append(entries, gitstore.IndexEntry{Mode: e.Mode, OID: e.OID, Path: p})
	}
	if err := os.MkdirAll(state.TmpDir(rc.Repo.GitDir), 0o700); err != nil {
		return nil, wrapErr(err, 1)
	}
	indexPath := filepath.Join(state.TmpDir(rc.Repo.GitDir), "idx-push-"+randomSuffix())

	subtree, err := rc.Store.BuildSubtree(ctx, entries, indexPath, opts.DryRun)
	if err != nil {
		return nil, wrapErr(err, 1)
	}
	rootTree, err := rc.Store.SpliceRoot(ctx, tip, rc.Config.Project.ID, subtree, len(local) > 0, opts.DryRun)
	if err != nil {
		return nil, wrapErr(err, 1)
	}
	res.Tree = rootTree
	res.Parent = tip

	if noOp(tip, rootTree, len(local) == 0, rc, ctx) {
		res.NoOp = true
		if opts.DryRun {
			return res, nil
		}
		// A no-op push can still finalize explicit resolutions.
		if len(resolved) > 0 {
			if err := finishPush(ctx, rc, tip, "", resolvedEntries); err != nil {
				return nil, err
			}
		}
		return res, nil
	}
	if opts.DryRun {
		return res, nil
	}

	message := commitMessage(rc, env, res)
	commit, err := rc.Store.CommitTree(ctx, rootTree, tip, message, signature(rc))
	if err != nil {
		return nil, wrapErr(err, 1)
	}
	res.Commit = commit
	if err := rc.Store.UpdateRef(ctx, "refs/doc-connector/pending", commit, ""); err != nil {
		return nil, wrapErr(err, 1)
	}

	expect := tip
	for attempt := range 3 {
		perr := rc.Store.Push(ctx, rc.Config.Remote, rc.Config.Branch, commit, expect)
		if perr == nil {
			return res, finishPush(ctx, rc, commit, commit, resolvedEntries)
		}
		var ge *gitstore.Error
		if !asGit(perr, &ge) || ge.Kind != gitstore.KindRace {
			return nil, wrapErr(perr, codeRemote)
		}

		// The remote moved: re-read it, prove the local scan did not change,
		// re-check clobber on the fresh tip, then rebuild on top of it.
		newTip, ferr := rc.Fetch(ctx, "push-retry")
		if ferr != nil {
			return nil, ferr
		}
		again, serr := scanForPush(ctx, rc, false, maxBytes, warnBytes, opts.Force, opts.FollowSymlinks)
		if serr != nil {
			return nil, serr
		}
		if !sameTree(local, again.Tree) {
			return nil, errf(codeConflict, "local documents changed during push; aborting without publishing").
				withHint("run push again when the directory is stable")
		}
		remote2, _, rerr := rc.RemoteTree(ctx, newTip)
		if rerr != nil {
			return nil, rerr
		}
		if err := rc.ensureMapped(remote2); err != nil {
			return nil, err
		}
		if clobber := trees.Clobber(base, local, remote2, resolved); len(clobber) > 0 {
			return nil, errf(codeConflict, "push refused after the remote moved: %s", strings.Join(clobber, ", ")).
				withHint("run \"doc-connector pull\" and retry")
		}
		rootTree2, berr := rc.Store.SpliceRoot(ctx, newTip, rc.Config.Project.ID, subtree, len(local) > 0, false)
		if berr != nil {
			return nil, wrapErr(berr, 1)
		}
		if noOp(newTip, rootTree2, len(local) == 0, rc, ctx) {
			return res, finishPush(ctx, rc, newTip, newTip, resolvedEntries)
		}
		commit2, cerr := rc.Store.CommitTree(ctx, rootTree2, newTip, message, signature(rc))
		if cerr != nil {
			return nil, wrapErr(cerr, 1)
		}
		commit = commit2
		res.Commit = commit
		res.Tree = rootTree2
		res.Parent = newTip
		expect = newTip
		if err := rc.Store.UpdateRef(ctx, "refs/doc-connector/pending", commit, ""); err != nil {
			return nil, wrapErr(err, 1)
		}
		time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
	}
	return nil, errf(codeRemote, "push retries exhausted after the remote kept moving").
		withHint("wait a moment and run push again")
}

func scanForPush(ctx context.Context, rc *RepoContext, write bool, maxBytes, warnBytes int64, force, followSymlinks bool) (*scan.Result, error) {
	return rc.scanAll(ctx, ScanPolicy{
		WarnBytes:      warnBytes,
		MaxBytes:       maxBytes,
		Force:          force,
		FollowSymlinks: followSymlinks,
	}, write)
}

// requireRoots refuses to sync when a configured docs directory is missing: a
// push from a partial view would delete the missing directory's remote paths.
func (rc *RepoContext) requireRoots() error {
	for _, e := range rc.Docs {
		if e.Root != nil {
			continue
		}
		return errf(codeGuard, "docs directory %q does not exist", e.Mapping.Path).
			withHint("create it and add documents before pushing; an empty push would remove the remote prefix")
	}
	return nil
}

func resolveConflicts(rc *RepoContext, requested []string) (map[string]bool, []state.Conflict, error) {
	resolved := map[string]bool{}
	var entries []state.Conflict
	if len(requested) == 0 {
		return resolved, entries, nil
	}
	known := map[string]state.Conflict{}
	for _, c := range rc.State.Unresolved {
		known[c.Path] = c
	}
	for _, p := range requested {
		c, ok := known[p]
		if !ok {
			return nil, nil, errf(codeUsage, "--resolve %q is not an unresolved conflict path", p).
				withHint("run status to see the current unresolved paths")
		}
		if resolved[p] {
			continue
		}
		resolved[p] = true
		entries = append(entries, c)
	}
	return resolved, entries, nil
}

// finishPush advances state and removes resolved artifacts only after the
// remote accepted the commit.
func finishPush(ctx context.Context, rc *RepoContext, syncedCommit, mirrorTip string, resolvedEntries []state.Conflict) error {
	if mirrorTip != "" {
		if err := rc.Store.UpdateRef(ctx, gitstore.RemoteBranch(rc.Config.Branch), mirrorTip, ""); err != nil {
			fmt.Fprintf(rc.warnWriter, "warning: cannot update the local mirror ref: %v\n", err)
		}
	}
	next := rc.State
	if syncedCommit != "" {
		next.SyncedCommit = syncedCommit
		oid, ok, err := rc.Store.TreeOf(ctx, syncedCommit, rc.Config.Project.ID)
		if err == nil && ok {
			next.BaselineTree = oid
		}
		next.LastSyncAt = state.Now()
		next.LastSyncKind = "push"
	}
	if len(resolvedEntries) > 0 {
		drop := map[string]bool{}
		for _, c := range resolvedEntries {
			drop[c.Path] = true
			if c.Theirs != "" {
				if err := removeConflictArtifact(rc, c.Theirs); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(rc.warnWriter, "warning: cannot remove conflict artifact %q: %v\n", c.Theirs, err)
				}
			}
		}
		kept := next.Unresolved[:0]
		for _, c := range next.Unresolved {
			if !drop[c.Path] {
				kept = append(kept, c)
			}
		}
		next.Unresolved = kept
	}
	if err := state.Save(rc.Repo.GitDir, next); err != nil {
		return wrapErr(err, 1)
	}
	rc.State = next
	if err := rc.Store.DeleteRef(ctx, "refs/doc-connector/pending"); err != nil {
		fmt.Fprintf(rc.warnWriter, "warning: cannot clear pending ref: %v\n", err)
	}
	return nil
}

func noOp(tip, rootTree string, emptyLocal bool, rc *RepoContext, ctx context.Context) bool {
	if tip == "" {
		return emptyLocal
	}
	t, err := rc.Store.TreeID(ctx, tip)
	if err != nil {
		return false
	}
	return t == rootTree
}

func commitMessage(rc *RepoContext, env Env, res *PushResult) string {
	files := fmt.Sprintf("+%d ~%d -%d (%d tracked)", res.Added, res.Modified, res.Deleted, res.Tracked)
	version := env.Version
	if version == "" {
		version = "dev"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "docs(%s): sync from %s\n\n", rc.Config.Project.ID, rc.Config.Machine)
	fmt.Fprintf(&b, "files: %s\n", files)
	fmt.Fprintf(&b, "host: %s\n", rc.Config.Machine)
	fmt.Fprintf(&b, "tool: doc-connector %s\n\n", version)
	fmt.Fprintf(&b, "Doc-Connector-Project: %s\n", rc.Config.Project.ID)
	fmt.Fprintf(&b, "Doc-Connector-Machine: %s\n", rc.Config.Machine)
	fmt.Fprintf(&b, "Doc-Connector-Files: +%d ~%d -%d\n", res.Added, res.Modified, res.Deleted)
	return b.String()
}

func signature(rc *RepoContext) gitstore.Signature {
	if rc.Config.Author != nil && rc.Config.Author.Name != "" {
		email := rc.Config.Author.Email
		if email == "" {
			email = "doc-connector@" + rc.Config.Machine
		}
		return gitstore.Signature{Name: rc.Config.Author.Name, Email: email}
	}
	return gitstore.Signature{Name: "doc-connector", Email: "doc-connector@" + rc.Config.Machine}
}

func diffCounts(local, remote trees.Tree) (added, modified, deleted int) {
	for p, e := range local {
		r, ok := remote[p]
		switch {
		case !ok:
			added++
		case !trees.Equal(e, r):
			modified++
		}
	}
	for p := range remote {
		if _, ok := local[p]; !ok {
			deleted++
		}
	}
	return added, modified, deleted
}

func sameTree(a, b trees.Tree) bool {
	if len(a) != len(b) {
		return false
	}
	for p, e := range a {
		if other, ok := b[p]; !ok || !trees.Equal(e, other) {
			return false
		}
	}
	return true
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func asGit(err error, target **gitstore.Error) bool {
	for err != nil {
		if e, ok := err.(*gitstore.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
