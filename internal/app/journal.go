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
	if rc.Root == nil {
		return errf(codeGuard, "journal exists but the docs directory is missing")
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
			if _, statErr := os.Stat(op.Staged); statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return errf(1, "recovery: cannot read staged file %q: %v", op.Staged, statErr)
			}
			mode := parseGitMode(op.Mode)
			if err := writeAtomicFromPath(rc.Root, target, op.Staged, mode); err != nil {
				return wrapErr(err, 1)
			}
		case "delete":
			abs := filepath.Join(rc.DocsDir, filepath.FromSlash(op.Path))
			if _, statErr := os.Lstat(abs); statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return errf(1, "recovery: cannot inspect %q: %v", op.Path, statErr)
			}
			trash := op.Staged
			if trash == "" {
				return errf(1, "recovery: delete of %q has no trash path", op.Path)
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
