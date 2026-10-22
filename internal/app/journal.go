package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/LZD121/doc-connector/internal/state"
)

// recoverJournal forward-recovers a pull that was interrupted. It is idempotent:
// writing an already-correct file rewrites identical bytes, deleting an absent
// file is a no-op, and an absent staged blob means the action already ran.
func recoverJournal(ctx context.Context, rc *RepoContext) error {
	j, err := state.LoadJournal(rc.Repo.GitDir)
	if err != nil {
		return wrapErr(err, 1)
	}
	if j == nil {
		return nil
	}
	rootMissing := false
	for _, e := range rc.Docs {
		if e.Root == nil {
			rootMissing = true
			break
		}
	}
	if rootMissing {
		return errf(codeGuard, "journal exists but a docs directory is missing").
			withHint("restore the docs directories to their configured paths, or inspect the journal and remove it if that pull is no longer wanted")
	}

	for _, op := range j.Ops {
		switch op.Action {
		case "write", "conflict":
			target := op.Path
			if op.Action == "conflict" {
				target = op.Theirs
			}
			if target == "" || op.Staged == "" {
				continue
			}
			entry, rel, rerr := rc.resolveProjectPath(target)
			if rerr != nil {
				return rerr
			}
			if _, statErr := os.Stat(op.Staged); statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return errf(1, "recovery: cannot read staged file %q: %v", op.Staged, statErr).
					withHint("the staged blob is gone; inspect the journal and re-run pull to rebuild it")
			}
			mode := parseGitMode(op.Mode)
			if err := writeAtomicFromPath(entry.Root, rel, op.Staged, mode); err != nil {
				return wrapErr(err, 1)
			}
		case "delete":
			entry, rel, rerr := rc.resolveProjectPath(op.Path)
			if rerr != nil {
				return rerr
			}
			abs := filepath.Join(entry.Abs, filepath.FromSlash(rel))
			if _, statErr := os.Lstat(abs); statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return errf(1, "recovery: cannot inspect %q: %v", op.Path, statErr).
					withHint("inspect that path manually; recovery will not overwrite a file it cannot read")
			}
			trash := op.Staged
			if trash == "" {
				return errf(1, "recovery: delete of %q has no trash path", op.Path).
					withHint("the journal is inconsistent; remove the journal file and re-run pull")
			}
			if err := moveToTrash(abs, trash); err != nil {
				return wrapErr(err, 1)
			}
		}
	}

	if j.StateNext != "" {
		var next state.State
		if err := state.ReadJSON(j.StateNext, &next); err == nil {
			if err := state.Save(rc.Repo.GitDir, next); err != nil {
				return wrapErr(err, 1)
			}
			rc.State = next
		}
	}
	if err := state.RemoveJournal(rc.Repo.GitDir); err != nil {
		return wrapErr(err, 1)
	}
	if j.TmpDir != "" {
		_ = os.RemoveAll(j.TmpDir)
	}
	return nil
}
