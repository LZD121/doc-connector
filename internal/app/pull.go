package app

import (
	"context"
	"os"
	"path/filepath"

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

// Pull applies remote additions, modifications and deletions to the docs
// directory. Any conflict class aborts before a single file is written.
func Pull(ctx context.Context, env Env, dryRun bool) (*PullResult, error) {
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: true})
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	if err := recoverJournal(ctx, rc); err != nil {
		return nil, err
	}
	// Recovery may have advanced state.
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

	actions, summary := trees.Classify(base, local.Tree, remote)
	res := &PullResult{Actions: actions, DryRun: dryRun, RemoteTip: tip}

	if summary.Conflicts > 0 {
		res.Conflicted = ConflictPaths(actions)
		return res, errf(codeConflict, "pull stopped: %d conflicting path(s); no files were written", len(res.Conflicted)).
			withHint("these paths changed both locally and remotely; conflict copies and push --resolve arrive in the next phase")
	}

	writes, deletes := planPull(actions)
	res.Writes = len(writes)
	res.Deletes = len(deletes)

	budget := rc.Config.Limits.MaxPathChars - len(rc.Config.DocsDir) - 1
	for _, a := range append(append([]trees.Action{}, writes...), deletes...) {
		if err := fsx.ValidateSyncPath(a.Path, budget); err != nil {
			return nil, wrapErr(err, codeGuard)
		}
	}
	for _, a := range writes {
		size, serr := rc.Store.BlobSize(ctx, a.Remote.OID)
		if serr != nil {
			return nil, wrapErr(serr, 1)
		}
		if size > rc.Config.Limits.MaxBytes {
			return nil, errf(1, "remote file %q is %d bytes, above the %d byte limit", a.Path, size, rc.Config.Limits.MaxBytes).
				withHint("large files must stay outside the synchronized docs directory")
		}
	}

	if dryRun || (len(writes) == 0 && len(deletes) == 0) {
		if !dryRun {
			if err := advanceBaseline(rc, tip, treeOID); err != nil {
				return nil, err
			}
		}
		return res, nil
	}

	appliedWrites, appliedDeletes, err := applyPull(ctx, rc, tip, treeOID, writes, deletes)
	res.Writes, res.Deletes = appliedWrites, appliedDeletes
	if err != nil {
		return res, err
	}
	return res, nil
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

func advanceBaseline(rc *RepoContext, tip, treeOID string) error {
	next := rc.State
	next.SyncedCommit = tip
	next.BaselineTree = treeOID
	next.LastSyncAt = state.Now()
	next.LastSyncKind = "pull"
	if err := state.Save(rc.Repo.GitDir, next); err != nil {
		return wrapErr(err, 1)
	}
	rc.State = next
	return nil
}

func applyPull(ctx context.Context, rc *RepoContext, tip, treeOID string, writes, deletes []trees.Action) (int, int, error) {
	opRoot := filepath.Join(state.TmpDir(rc.Repo.GitDir), "op-"+randomSuffix())
	filesDir := filepath.Join(opRoot, "files")
	trashDir := filepath.Join(opRoot, "trash")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return 0, 0, wrapErr(err, 1)
	}
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return 0, 0, wrapErr(err, 1)
	}

	oids := make([]string, 0, len(writes))
	for _, a := range writes {
		oids = append(oids, a.Remote.OID)
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
	ops := make([]state.JournalOp, 0, len(writes)+len(deletes))
	for _, a := range writes {
		ops = append(ops, state.JournalOp{
			Action: "write",
			Path:   a.Path,
			Mode:   modeString(a.Remote.Mode),
			OID:    a.Remote.OID,
			Staged: staged[a.Remote.OID],
		})
	}
	for _, a := range deletes {
		ops = append(ops, state.JournalOp{
			Action: "delete",
			Path:   a.Path,
			Staged: filepath.Join(trashDir, filepath.FromSlash(a.Path)),
		})
	}
	journal.Ops = ops

	nextState := rc.State
	nextState.SyncedCommit = tip
	nextState.BaselineTree = treeOID
	nextState.LastSyncAt = state.Now()
	nextState.LastSyncKind = "pull"
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
