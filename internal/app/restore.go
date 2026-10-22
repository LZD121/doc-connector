package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LZD121/doc-connector/internal/fsx"
	"github.com/LZD121/doc-connector/internal/state"
)

// RestoreOptions selects a historical blob.
type RestoreOptions struct {
	Commit string
	From   string
	Path   string
	To     string
	Force  bool
}

// RestoreResult reports a restore.
type RestoreResult struct {
	Path             string
	Commit           string
	OID              string
	Artifact         string
	Unresolved       bool
	OverwriteBlocked bool
}

// Restore writes a historical version of one path back into the docs directory.
// It never commits and never advances the baseline.
func Restore(ctx context.Context, env Env, opts RestoreOptions) (*RestoreResult, error) {
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: opts.To == ""})
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	entry, rel, err := rc.resolveUserPath(opts.Path)
	if err != nil {
		return nil, err
	}
	if err := fsx.ValidateSyncPath(rel, entry.Budget); err != nil {
		return nil, wrapErr(err, codeGuard)
	}
	projectRel := projectPath(entry.Mapping.Prefix, rel)

	rev := opts.Commit
	if rev == "" {
		rev = opts.From
	}
	if rev == "" {
		tip, rerr := rc.Store.RefTip(ctx, "refs/remotes/origin/"+rc.Config.Branch)
		if rerr != nil {
			return nil, wrapErr(rerr, 1)
		}
		if tip == "" {
			return nil, errf(codeUsage, "no fetched remote tip to restore from").
				withHint("pass an explicit commit, or run status once while online")
		}
		rev = tip
	}
	commit, cerr := rc.Store.Run(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if cerr != nil {
		return nil, errf(codeUsage, "unknown revision %q", rev).
			withHint("list commits with \"doc-connector log\" and pass a revision shown there")
	}
	commit = strings.TrimSpace(commit)

	oid, mode, berr := blobAt(ctx, rc, commit, projectRel)
	if berr != nil {
		return nil, berr
	}

	staged, serr := stageBlob(ctx, rc, oid)
	if serr != nil {
		return nil, serr
	}
	res := &RestoreResult{Path: projectRel, Commit: commit, OID: oid}

	if opts.To != "" {
		target := filepath.Join(opts.To, filepath.FromSlash(projectRel))
		if err := writeAtomicAbs(target, staged, parseGitMode(mode)); err != nil {
			return nil, wrapErr(err, 1)
		}
		return res, nil
	}
	if entry.Root == nil {
		return nil, errf(codeGuard, "docs directory %q is missing", entry.Mapping.Path).
			withHint("re-create the configured docs directory, then retry")
	}

	if _, statErr := entry.Root.Lstat(rel); statErr == nil && !opts.Force {
		baseline, lerr := rc.Baseline(ctx)
		if lerr != nil {
			return nil, lerr
		}
		oids, herr := rc.Store.HashPaths(ctx, entry.Abs, []string{rel}, false)
		if herr != nil {
			return nil, wrapErr(herr, 1)
		}
		b, ok := baseline[projectRel]
		if !ok || b.OID != strings.TrimSpace(oids[0]) {
			return nil, errf(codeConflict, "%q has unsynced local changes", projectRel).
				withHint("re-run with --force to overwrite them (the old bytes remain recoverable from the mirror)")
		}
	}

	if err := writeAtomicFromPath(entry.Root, rel, staged, parseGitMode(mode)); err != nil {
		return nil, wrapErr(err, 1)
	}

	if opts.Force {
		st, lerr := state.Load(rc.Repo.GitDir)
		if lerr != nil {
			return nil, wrapErr(lerr, 1)
		}
		var kept []state.Conflict
		changed := false
		for _, c := range st.Unresolved {
			if c.Path != projectRel {
				kept = append(kept, c)
				continue
			}
			changed = true
			if c.Theirs != "" {
				res.Artifact = c.Theirs
				if err := removeConflictArtifact(rc, c.Theirs); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(rc.warnWriter, "warning: cannot remove conflict artifact %q: %v\n", c.Theirs, err)
				}
			}
			if oid == c.RemoteOID {
				res.Unresolved = true
				continue
			}
			kept = append(kept, c)
		}
		if changed {
			st.Unresolved = kept
			if err := state.Save(rc.Repo.GitDir, st); err != nil {
				return nil, wrapErr(err, 1)
			}
		}
	}
	return res, nil
}

// blobAt resolves the blob at <commit>:<project_id>/<ppath>, falling back to
// the last commit that touched the path when the file is gone at that
// revision. ppath is a project path, so it carries the mapping prefix, if any.
func blobAt(ctx context.Context, rc *RepoContext, commit, ppath string) (string, string, error) {
	prefix := rc.Config.Project.ID + "/" + ppath
	spec := commit + ":" + prefix

	out, err := rc.Store.Run(ctx, "rev-parse", "--verify", "--quiet", spec)
	if err != nil {
		last, lerr := rc.Store.Run(ctx, "log", "--format=%H", "-1", commit, "--", prefix)
		if lerr != nil {
			return "", "", wrapErr(lerr, 1)
		}
		last = strings.TrimSpace(last)
		if last == "" {
			return "", "", errf(codeUsage, "path %q has no history at %s", ppath, shortOID(commit)).
				withHint("check the path with \"doc-connector log --path %s\"", ppath)
		}
		commit = last
		spec = commit + ":" + prefix
		out, err = rc.Store.Run(ctx, "rev-parse", "--verify", "--quiet", spec)
		if err != nil {
			return "", "", errf(codeUsage, "path %q could not be read from %s", ppath, shortOID(commit)).
				withHint("confirm the path still exists at that commit with \"doc-connector log --path %s\"", ppath)
		}
	}
	oid := strings.TrimSpace(out)
	kind, terr := rc.Store.Run(ctx, "cat-file", "-t", oid)
	if terr != nil {
		return "", "", wrapErr(terr, 1)
	}
	if strings.TrimSpace(kind) != "blob" {
		return "", "", errf(codeGuard, "path %q is not a file at %s", ppath, shortOID(commit)).
			withHint("restore reads plain files only; directories and symlinks cannot be restored")
	}

	mode := "100644"
	listing, lerr := rc.Store.Run(ctx, "ls-tree", commit, "--", prefix)
	if lerr == nil {
		if fields := strings.Fields(listing); len(fields) >= 1 {
			mode = fields[0]
		}
	}
	return oid, mode, nil
}

func stageBlob(ctx context.Context, rc *RepoContext, oid string) (string, error) {
	dir := filepath.Join(state.TmpDir(rc.Repo.GitDir), "restore-"+randomSuffix())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", wrapErr(err, 1)
	}
	paths, err := rc.Store.WriteBlobs(ctx, []string{oid}, dir)
	if err != nil {
		return "", wrapErr(err, 1)
	}
	return paths[oid], nil
}
