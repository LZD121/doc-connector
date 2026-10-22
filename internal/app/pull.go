package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/LZD121/doc-connector/internal/conflict"
	"github.com/LZD121/doc-connector/internal/fsx"
	"github.com/LZD121/doc-connector/internal/state"
	"github.com/LZD121/doc-connector/internal/trees"
)

// PullResult reports what pull did (or would do).
type PullResult struct {
	Actions    []trees.Action
	Writes     int
	Deletes    int
	Conflicted []string
	DryRun     bool
	RemoteTip  string
}

type conflictPlan struct {
	Path   string
	Theirs string
	OID    string
	Entry  state.Conflict
}

// Pull applies remote additions, modifications and deletions to the docs
// directory. Conflicts keep the local file, materialize the remote version as
// <path>.theirs.<machine>, and exit 1 after applying the non-conflicting work.
func Pull(ctx context.Context, env Env, dryRun bool) (*PullResult, error) {
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: true})
	if err != nil {
		return nil, err
	}
	defer rc.Close()

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

	local, err := rc.ScanLocal(ctx)
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

	actions, _ := trees.Classify(base, local.Tree, remote)
	res := &PullResult{Actions: actions, DryRun: dryRun, RemoteTip: tip}

	writes, deletes := planPull(actions)
	res.Writes = len(writes)
	res.Deletes = len(deletes)

	existing := map[string]state.Conflict{}
	for _, c := range rc.State.Unresolved {
		existing[c.Path] = c
	}
	budget := rc.Config.Limits.MaxPathChars - len(rc.Config.DocsDir) - 1
	var plans []conflictPlan
	for _, a := range actions {
		if a.Status != "C" {
			continue
		}
		res.Conflicted = append(res.Conflicted, a.Path)
		if _, seen := existing[a.Path]; seen {
			continue
		}
		plan := conflictPlan{Path: a.Path, Entry: state.Conflict{
			Path: a.Path,
			Kind: conflict.Kind(a.Class.String()),
			At:   state.Now(),
		}}
		if a.Remote != nil {
			theirs, aerr := conflict.ArtifactPath(rc.Root, a.Path, rc.Config.Machine, budget)
			if aerr != nil {
				return nil, wrapErr(aerr, codeGuard)
			}
			plan.Theirs = theirs
			plan.OID = a.Remote.OID
			plan.Entry.Theirs = theirs
			plan.Entry.RemoteOID = a.Remote.OID
		}
		if a.Base != nil {
			plan.Entry.BaseOID = a.Base.OID
		}
		plans = append(plans, plan)
	}

	for _, a := range append(append([]trees.Action{}, writes...), deletes...) {
		if err := fsx.ValidateSyncPath(a.Path, budget); err != nil {
			return nil, wrapErr(err, codeGuard)
		}
	}
	for _, a := range writes {
		if err := checkRemoteSize(ctx, rc, a.Path, a.Remote.OID); err != nil {
			return nil, err
		}
	}
	for _, p := range plans {
		if p.OID == "" {
			continue
		}
		if err := checkRemoteSize(ctx, rc, p.Path, p.OID); err != nil {
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

func checkRemoteSize(ctx context.Context, rc *RepoContext, pathName, oid string) error {
	size, err := rc.Store.BlobSize(ctx, oid)
	if err != nil {
		return wrapErr(err, 1)
	}
	if size > rc.Config.Limits.MaxBytes {
		return errf(1, "remote file %q is %d bytes, above the %d byte limit", pathName, size, rc.Config.Limits.MaxBytes).
			withHint("large files must stay outside the synchronized docs directory")
	}
	return nil
}

func planPull(actions []trees.Action) (writes, deletes []trees.Action) {
	for _, a := range actions {
		switch a.Class {
		case trees.RemoteAdd, trees.RemoteMod:
			writes = append(writes, a)
		case trees.RemoteDelete:
			deletes = append(deletes, a)
		}
	}
	return writes, deletes
}

func applyPull(ctx context.Context, rc *RepoContext, tip, treeOID string, writes, deletes []trees.Action, plans []conflictPlan, existing map[string]state.Conflict) (int, int, error) {
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
		oids = append(oids, a.Remote.OID)
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
	ops := make([]state.JournalOp, 0, len(writes)+len(deletes)+len(plans))
	for _, a := range writes {
		ops = append(ops, state.JournalOp{
			Action: "write", Path: a.Path, Mode: modeString(a.Remote.Mode),
			OID: a.Remote.OID, Staged: staged[a.Remote.OID],
		})
	}
	for _, a := range deletes {
		ops = append(ops, state.JournalOp{
			Action: "delete", Path: a.Path,
			Staged: filepath.Join(trashDir, filepath.FromSlash(a.Path)),
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
		nextState.Unresolved = append(nextState.Unresolved, p.Entry)
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
		if err := writeAtomicFromPath(rc.Root, a.Path, staged[a.Remote.OID], parseGitMode(modeString(a.Remote.Mode))); err != nil {
			return appliedWrites, 0, wrapErr(err, 1)
		}
		appliedWrites++
	}

	appliedDeletes := 0
	for _, a := range deletes {
		abs := filepath.Join(rc.DocsDir, filepath.FromSlash(a.Path))
		trash := filepath.Join(trashDir, filepath.FromSlash(a.Path))
		if err := moveToTrash(abs, trash); err != nil {
			return appliedWrites, appliedDeletes, wrapErr(err, 1)
		}
		appliedDeletes++
	}
	cleanupEmptyDirs(rc.DocsDir, actionPaths(deletes))

	for _, p := range plans {
		if p.Theirs == "" || p.OID == "" {
			continue
		}
		if err := writeAtomicFromPath(rc.Root, p.Theirs, staged[p.OID], 0o644); err != nil {
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

func actionPaths(actions []trees.Action) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = a.Path
	}
	return out
}
