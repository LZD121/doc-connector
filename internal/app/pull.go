package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/LZD121/doc-connector/internal/conflict"
	"github.com/LZD121/doc-connector/internal/fsx"
	"github.com/LZD121/doc-connector/internal/ignore"
	"github.com/LZD121/doc-connector/internal/state"
	"github.com/LZD121/doc-connector/internal/trees"
)

// PullOptions controls one pull.
type PullOptions struct {
	DryRun         bool
	Force          bool
	AllowLarge     bool
	FollowSymlinks bool
}

// PullResult reports what pull did (or would do).
type PullResult struct {
	Actions    []trees.Action
	Writes     int
	Deletes    int
	Conflicted []string
	DryRun     bool
	RemoteTip  string
}

// resolvedAction is one classified path bound to the docs directory that owns
// it: Action.Path is the project path, Rel the path inside Docs.Abs.
type resolvedAction struct {
	Action trees.Action
	Docs   docsEntry
	Rel    string
}

type conflictPlan struct {
	Path      string // project path recorded in state and shown to the user
	Rel       string // path inside Docs.Root
	Docs      docsEntry
	Theirs    string // project path of the artifact, recorded in state
	TheirsRel string // artifact path inside Docs.Root
	OID       string
	Conflict  state.Conflict
}

// Pull applies remote additions, modifications and deletions to the docs
// directory. Conflicts keep the local file, materialize the remote version as
// <path>.theirs.<machine>, and exit 1 after applying the non-conflicting work.
func Pull(ctx context.Context, env Env, opts PullOptions) (*PullResult, error) {
	dryRun := opts.DryRun
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: true})
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// Dry-run takes the lock too: it reads the baseline and the remote tip.
	release, err := acquireProjectLock(ctx, rc, "pull")
	if err != nil {
		return nil, err
	}
	defer release()

	if err := recoverJournal(ctx, rc); err != nil {
		return nil, err
	}
	if st, lerr := state.Load(rc.Repo.GitDir); lerr == nil {
		rc.State = st
	}

	tip, err := rc.Fetch(ctx, "pull")
	if err != nil {
		return nil, err
	}
	if tip == "" && rc.State.SyncedCommit != "" {
		return nil, errf(codeConflict, "remote branch %q no longer exists", rc.Config.Branch).
			withHint("the remote history may have been rewritten; restore it or re-init explicitly, doc-connector will not delete local docs")
	}

	maxBytes := rc.Config.Limits.MaxBytes
	if opts.AllowLarge {
		maxBytes = 0
	}
	local, err := rc.ScanLocal(ctx, ScanPolicy{
		WarnBytes:      rc.Config.Limits.WarnBytes,
		MaxBytes:       maxBytes,
		Force:          opts.Force,
		FollowSymlinks: opts.FollowSymlinks,
	})
	if err != nil {
		return nil, err
	}
	base, err := rc.Baseline(ctx)
	if err != nil {
		return nil, err
	}
	remote, treeOID, err := rc.RemoteTree(ctx, tip)
	if err != nil {
		return nil, err
	}

	localTree := local.Tree
	if runtime.GOOS == "windows" {
		localTree = trees.NormalizeModes(localTree, base, remote)
	}
	actions, _ := trees.Classify(base, localTree, remote)
	// Every classified path must belong to a configured mapping, and .docignore
	// is a local-only control: a path it hides must never be written, deleted or
	// turned into a conflict by a pull, even while the remote still tracks it.
	// Resolving up front means an unmapped path aborts before any write.
	resolved, unmapped := rc.resolveActions(actions)
	if len(unmapped) > 0 {
		return nil, rc.unmappedError(unmapped)
	}
	res := &PullResult{DryRun: dryRun, RemoteTip: tip}
	res.Actions = make([]trees.Action, 0, len(resolved))
	for _, ra := range resolved {
		res.Actions = append(res.Actions, ra.Action)
	}

	writes, deletes := planPull(resolved)
	res.Writes = len(writes)
	res.Deletes = len(deletes)

	existing := map[string]state.Conflict{}
	for _, c := range rc.State.Unresolved {
		existing[c.Path] = c
	}
	var plans []conflictPlan
	for _, ra := range resolved {
		if ra.Action.Status != "C" {
			continue
		}
		res.Conflicted = append(res.Conflicted, ra.Action.Path)
		if _, seen := existing[ra.Action.Path]; seen {
			continue
		}
		plan := conflictPlan{
			Path: ra.Action.Path,
			Rel:  ra.Rel,
			Docs: ra.Docs,
			Conflict: state.Conflict{
				Path: ra.Action.Path,
				Kind: conflict.Kind(ra.Action.Class.String()),
				At:   state.Now(),
			},
		}
		if ra.Action.Remote != nil {
			if ra.Docs.Root == nil {
				return nil, errf(codeGuard, "docs directory %q is missing", ra.Docs.Mapping.Path).
					withHint("re-create it, then run pull again")
			}
			theirsRel, aerr := conflict.ArtifactPath(ra.Docs.Root, ra.Rel, rc.Config.Machine, ra.Docs.Budget)
			if aerr != nil {
				return nil, wrapErr(aerr, codeGuard)
			}
			plan.TheirsRel = theirsRel
			plan.Theirs = projectPath(ra.Docs.Mapping.Prefix, theirsRel)
			plan.OID = ra.Action.Remote.OID
			plan.Conflict.Theirs = plan.Theirs
			plan.Conflict.RemoteOID = ra.Action.Remote.OID
		}
		if ra.Action.Base != nil {
			plan.Conflict.BaseOID = ra.Action.Base.OID
		}
		plans = append(plans, plan)
	}

	for _, ra := range append(append([]resolvedAction{}, writes...), deletes...) {
		if err := fsx.ValidateSyncPath(ra.Rel, ra.Docs.Budget); err != nil {
			return nil, wrapErr(err, codeGuard)
		}
	}
	for _, ra := range writes {
		if err := checkRemoteSize(ctx, rc, ra.Action.Path, ra.Action.Remote.OID, opts.Force, maxBytes); err != nil {
			return nil, err
		}
	}
	for _, p := range plans {
		if p.OID == "" {
			continue
		}
		if err := checkRemoteSize(ctx, rc, p.Path, p.OID, opts.Force, maxBytes); err != nil {
			return nil, err
		}
	}

	if dryRun {
		if len(res.Conflicted) > 0 {
			return res, errf(codeConflict, "dry-run: pull would keep %d conflicting path(s) and write .theirs copies", len(res.Conflicted)).
				withHint("no files were written")
		}
		return res, nil
	}

	appliedWrites, appliedDeletes, applyErr := applyPull(ctx, rc, tip, treeOID, writes, deletes, plans, existing)
	res.Writes, res.Deletes = appliedWrites, appliedDeletes
	if applyErr != nil {
		return res, applyErr
	}
	if len(res.Conflicted) > 0 {
		return res, errf(codeConflict, "pull applied remote changes and kept %d conflicting path(s)", len(res.Conflicted)).
			withHint("merge each conflict, then run push --resolve <path>; the baseline stays put until every conflict is resolved")
	}
	return res, nil
}

func checkRemoteSize(ctx context.Context, rc *RepoContext, pathName, oid string, force bool, maxBytes int64) error {
	size, err := rc.Store.BlobSize(ctx, oid)
	if err != nil {
		return wrapErr(err, 1)
	}
	if maxBytes > 0 && size > maxBytes {
		return errf(1, "remote file %q is %d bytes, above the %d byte hard limit", pathName, size, maxBytes).
			withHint("large files must stay outside the synchronized docs directory")
	}
	if size > rc.Config.Limits.WarnBytes && !force {
		return errf(1, "remote file %q is %d bytes, above the %d byte warning limit", pathName, size, rc.Config.Limits.WarnBytes).
			withHint("re-run with --force to synchronize it anyway")
	}
	if size > rc.Config.Limits.WarnBytes {
		fmt.Fprintf(rc.warnWriter, "warning: %s is %d bytes; --force was used\n", pathName, size)
	}
	return nil
}

// resolveActions binds every classified path to its docs directory and drops
// actions for paths the local .docignore rules hide. Unmapped paths are
// returned so the caller can refuse the whole pull before any write.
func (rc *RepoContext) resolveActions(actions []trees.Action) ([]resolvedAction, []string) {
	resolved := make([]resolvedAction, 0, len(actions))
	var unmapped []string
	for _, a := range actions {
		entry, rel, err := rc.resolveProjectPath(a.Path)
		if err != nil {
			unmapped = append(unmapped, a.Path)
			continue
		}
		if rc.Filter != nil && rc.Filter.Match(rel, false) {
			continue
		}
		resolved = append(resolved, resolvedAction{Action: a, Docs: entry, Rel: rel})
	}
	return resolved, unmapped
}

// dropIgnoredActions removes actions for paths the local .docignore rules hide.
func dropIgnoredActions(filter *ignore.Filter, actions []resolvedAction) []resolvedAction {
	if filter == nil || len(actions) == 0 {
		return actions
	}
	kept := actions[:0]
	for _, a := range actions {
		if filter.Match(a.Rel, false) {
			continue
		}
		kept = append(kept, a)
	}
	return kept
}

// dropIgnoredPlans removes materialized conflicts for paths the local
// .docignore rules hide: an ignored file must not gain a .theirs copy or an
// unresolved entry that would block push forever.
func dropIgnoredPlans(filter *ignore.Filter, plans []conflictPlan) []conflictPlan {
	if filter == nil || len(plans) == 0 {
		return plans
	}
	kept := plans[:0]
	for _, p := range plans {
		if filter.Match(p.Rel, false) {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

func planPull(actions []resolvedAction) (writes, deletes []resolvedAction) {
	for _, a := range actions {
		switch a.Action.Class {
		case trees.RemoteAdd, trees.RemoteMod:
			writes = append(writes, a)
		case trees.RemoteDelete:
			deletes = append(deletes, a)
		}
	}
	return writes, deletes
}

func applyPull(ctx context.Context, rc *RepoContext, tip, treeOID string, writes, deletes []resolvedAction, plans []conflictPlan, existing map[string]state.Conflict) (int, int, error) {
	// A pull must never write or delete a path the local .docignore hides.
	// Pull drops those actions before planning; this keeps the apply loop safe
	// on its own even if a caller ever hands it an unfiltered plan.
	writes = dropIgnoredActions(rc.Filter, writes)
	deletes = dropIgnoredActions(rc.Filter, deletes)
	plans = dropIgnoredPlans(rc.Filter, plans)

	opRoot := filepath.Join(state.TmpDir(rc.Repo.GitDir), "op-"+randomSuffix())
	filesDir := filepath.Join(opRoot, "files")
	trashDir := filepath.Join(opRoot, "trash")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return 0, 0, wrapErr(err, 1)
	}
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return 0, 0, wrapErr(err, 1)
	}

	oids := make([]string, 0, len(writes)+len(plans))
	for _, a := range writes {
		oids = append(oids, a.Action.Remote.OID)
	}
	for _, p := range plans {
		if p.OID != "" {
			oids = append(oids, p.OID)
		}
	}
	staged, err := rc.Store.WriteBlobs(ctx, oids, filesDir)
	if err != nil {
		return 0, 0, wrapErr(err, 1)
	}

	journal := &state.Journal{
		Schema:       state.SchemaVersion,
		Op:           "pull",
		StartedAt:    state.Now(),
		ProjectID:    rc.Config.Project.ID,
		RemoteCommit: tip,
		TmpDir:       opRoot,
	}
	// Journal paths are project paths, so recovery re-resolves them against the
	// same configuration instead of assuming one docs directory.
	ops := make([]state.JournalOp, 0, len(writes)+len(deletes)+len(plans))
	for _, a := range writes {
		ops = append(ops, state.JournalOp{
			Action: "write", Path: a.Action.Path, Mode: modeString(a.Action.Remote.Mode),
			OID: a.Action.Remote.OID, Staged: staged[a.Action.Remote.OID],
		})
	}
	for _, a := range deletes {
		ops = append(ops, state.JournalOp{
			Action: "delete", Path: a.Action.Path,
			Staged: filepath.Join(trashDir, filepath.FromSlash(a.Docs.Mapping.Path), filepath.FromSlash(a.Rel)),
		})
	}
	for _, p := range plans {
		if p.Theirs == "" || p.OID == "" {
			continue
		}
		ops = append(ops, state.JournalOp{
			Action: "conflict", Path: p.Path, Theirs: p.Theirs,
			Mode: "100644", OID: p.OID, Staged: staged[p.OID],
		})
	}
	journal.Ops = ops

	nextState := rc.State
	nextState.Unresolved = append([]state.Conflict{}, rc.State.Unresolved...)
	for _, p := range plans {
		nextState.Unresolved = append(nextState.Unresolved, p.Conflict)
	}
	// The baseline advances only when nothing is unresolved.
	if len(nextState.Unresolved) == 0 {
		nextState.SyncedCommit = tip
		nextState.BaselineTree = treeOID
		nextState.LastSyncAt = state.Now()
		nextState.LastSyncKind = "pull"
	}
	stateNext := filepath.Join(opRoot, "state.next.json")
	journal.StateNext = stateNext
	if err := state.WriteJSON(stateNext, nextState); err != nil {
		return 0, 0, wrapErr(err, 1)
	}
	if err := state.SaveJournal(rc.Repo.GitDir, journal); err != nil {
		return 0, 0, wrapErr(err, 1)
	}

	appliedWrites := 0
	for _, a := range writes {
		if err := writeAtomicFromPath(a.Docs.Root, a.Rel, staged[a.Action.Remote.OID], parseGitMode(modeString(a.Action.Remote.Mode))); err != nil {
			return appliedWrites, 0, wrapErr(err, 1)
		}
		appliedWrites++
	}

	appliedDeletes := 0
	deleted := map[string][]string{}
	for _, a := range deletes {
		abs := filepath.Join(a.Docs.Abs, filepath.FromSlash(a.Rel))
		trash := filepath.Join(trashDir, filepath.FromSlash(a.Docs.Mapping.Path), filepath.FromSlash(a.Rel))
		if err := moveToTrash(abs, trash); err != nil {
			return appliedWrites, appliedDeletes, wrapErr(err, 1)
		}
		deleted[a.Docs.Abs] = append(deleted[a.Docs.Abs], a.Rel)
		appliedDeletes++
	}
	for dir, rels := range deleted {
		cleanupEmptyDirs(dir, rels)
	}

	for _, p := range plans {
		if p.Theirs == "" || p.OID == "" {
			continue
		}
		if err := writeAtomicFromPath(p.Docs.Root, p.TheirsRel, staged[p.OID], 0o644); err != nil {
			return appliedWrites, appliedDeletes, wrapErr(err, 1)
		}
	}

	if err := state.Save(rc.Repo.GitDir, nextState); err != nil {
		return appliedWrites, appliedDeletes, wrapErr(err, 1)
	}
	rc.State = nextState
	if err := state.RemoveJournal(rc.Repo.GitDir); err != nil {
		return appliedWrites, appliedDeletes, wrapErr(err, 1)
	}
	_ = os.RemoveAll(opRoot)
	return appliedWrites, appliedDeletes, nil
}
