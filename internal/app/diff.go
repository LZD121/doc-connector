package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/state"
	"github.com/LZD121/doc-connector/internal/trees"
)

// DiffOptions selects the revision to start from and an optional path.
type DiffOptions struct {
	Commit string
	From   string
	Path   string
}

// DiffFile is one file in a diff of the project prefix. Path is relative to
// the project prefix, so it reads the same way as every other user-facing path.
type DiffFile struct {
	Path       string
	Status     string // A (added), D (deleted) or M (modified)
	Insertions int
	Deletions  int
	Patch      string
	Truncated  bool
}

// DiffResult is one diff of the project prefix against the local docs tree.
//
// To is the latest synced revision the mirror knows: the remote-tracking tip,
// or the last synced commit when the ref is gone. It labels the revision the
// local tree was last known to match; the comparison itself runs the base
// subtree against the files on disk, so uncommitted edits still appear.
type DiffResult struct {
	From  string // full OID of the resolved base revision
	To    string // full OID of the latest synced revision
	Path  string // user path filter, empty when the whole prefix is shown
	Files []DiffFile
	Raw   string // the rewritten unified patch, byte for byte
}

// maxPatchBytes bounds one DiffFile.Patch so a huge document cannot flood a
// JSON consumer. Raw always carries the complete patch.
const maxPatchBytes = 64 << 10

// Diff shows how one historical version of the project prefix differs from the
// local docs tree, including edits that were never pushed. It reads the local
// mirror and the docs directories only: no fetch, no ref or object writes, no
// state writes.
//
// The mirror is bare and a docs directory is not a git work tree, so git cannot
// compare a tree with the filesystem in one invocation. The command therefore
// reads the base revision's <project_id> subtree, scans the docs directories
// into the same project paths, and pairs every changed path with two files in a
// private scratch directory: the base blob (written with git cat-file) or an
// empty stand-in when the path is new, and the real local file or an empty
// stand-in when the path is gone. `git diff --no-index` renders each pair, and
// the scratch names in the header are rewritten to the project path, so the
// temp directory never leaks into output. A revision with no <project_id> entry
// contributes an empty tree, so a pre-docs commit reads as the whole prefix
// being added instead of failing with a bare "bad object".
func Diff(ctx context.Context, env Env, opts DiffOptions) (*DiffResult, error) {
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: false})
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	filter, err := diffFilter(rc, opts.Path)
	if err != nil {
		return nil, err
	}

	base, err := diffBase(ctx, rc, opts)
	if err != nil {
		return nil, err
	}
	target, err := diffTarget(ctx, rc)
	if err != nil {
		return nil, err
	}

	baseTree, err := diffBaseTree(ctx, rc, base)
	if err != nil {
		return nil, err
	}
	scanned, err := rc.ScanLocal(ctx, ScanPolicy{})
	if err != nil {
		return nil, err
	}

	res := &DiffResult{From: base, To: target, Path: opts.Path, Files: []DiffFile{}}
	changes := diffChanges(baseTree, scanned.Tree, filter)
	if len(changes) == 0 {
		return res, nil
	}
	res.Files, res.Raw, err = diffPatches(ctx, rc, changes)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// diffFilter turns the optional user path into a project-path filter. A name
// that equals a configured prefix selects that whole mapping, matching the
// subtree pathspec the command accepted before it read the work tree;
// otherwise the path resolves like every other user path.
func diffFilter(rc *RepoContext, p string) (string, error) {
	if p == "" {
		return "", nil
	}
	for _, e := range rc.Docs {
		if e.Mapping.Prefix != "" && p == e.Mapping.Prefix {
			return p, nil
		}
	}
	entry, rel, err := rc.resolveUserPath(p)
	if err != nil {
		return "", err
	}
	if err := validatePath(rel, entry.Budget); err != nil {
		return "", wrapErr(err, codeGuard)
	}
	return projectPath(entry.Mapping.Prefix, rel), nil
}

// diffBaseTree reads the base revision's project subtree. A commit that
// predates the prefix has no <project_id> entry; it contributes an empty tree.
func diffBaseTree(ctx context.Context, rc *RepoContext, commit string) (trees.Tree, error) {
	_, ok, err := rc.Store.TreeOf(ctx, commit, rc.Config.Project.ID)
	if err != nil {
		return nil, wrapErr(err, 1)
	}
	if !ok {
		return trees.Tree{}, nil
	}
	tree, err := trees.ReadTree(ctx, rc.Store, commit+":"+rc.Config.Project.ID)
	if err != nil {
		return nil, wrapErr(err, 1)
	}
	return tree, nil
}

// diffChange is one project path whose base and local entries differ.
type diffChange struct {
	Path     string
	Status   string // A (added), D (deleted) or M (modified)
	Base     trees.Entry
	HasBase  bool
	Local    trees.Entry
	HasLocal bool
}

// diffChanges lists changed project paths in sorted order, so the patch is
// deterministic. filter is a project path or a directory prefix of one; empty
// means the whole prefix.
func diffChanges(base, local trees.Tree, filter string) []diffChange {
	seen := make(map[string]struct{}, len(base)+len(local))
	paths := make([]string, 0, len(base)+len(local))
	for _, tree := range []trees.Tree{base, local} {
		for p := range tree {
			if !diffPathMatches(p, filter) {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	changes := make([]diffChange, 0, len(paths))
	for _, p := range paths {
		b, hasBase := base[p]
		l, hasLocal := local[p]
		change := diffChange{Path: p, Base: b, HasBase: hasBase, Local: l, HasLocal: hasLocal}
		switch {
		case hasBase && !hasLocal:
			change.Status = "D"
		case !hasBase && hasLocal:
			change.Status = "A"
		case b.OID != l.OID:
			change.Status = "M"
		default:
			continue
		}
		changes = append(changes, change)
	}
	return changes
}

// diffPathMatches applies the user path filter: a path names itself and, when
// it is a directory, everything below it.
func diffPathMatches(p, filter string) bool {
	return filter == "" || p == filter || strings.HasPrefix(p, filter+"/")
}

// diffPatches renders one patch per change. Base blobs and both empty stand-ins
// live in a scratch directory under state.TmpDir, which is removed on every
// return path; the local side is the real file on disk and is only read.
func diffPatches(ctx context.Context, rc *RepoContext, changes []diffChange) ([]DiffFile, string, error) {
	dir := filepath.Join(state.TmpDir(rc.Repo.GitDir), "diff-"+randomSuffix())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", wrapErr(err, 1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		return nil, "", wrapErr(err, 1)
	}

	oids := make([]string, 0, len(changes))
	for _, ch := range changes {
		if ch.HasBase {
			oids = append(oids, ch.Base.OID)
		}
	}
	blobs, err := rc.Store.WriteBlobs(ctx, oids, dir)
	if err != nil {
		return nil, "", wrapErr(err, 1)
	}

	files := make([]DiffFile, 0, len(changes))
	var raw strings.Builder
	for _, ch := range changes {
		baseTmp := empty
		if ch.HasBase {
			p, ok := blobs[ch.Base.OID]
			if !ok {
				return nil, "", errf(1, "base blob %s for %q was not extracted", shortOID(ch.Base.OID), ch.Path).
					withHint("run pull once while online to refill the local mirror")
			}
			baseTmp = p
		}
		localTmp := empty
		if ch.HasLocal {
			entry, rel, rerr := rc.resolveProjectPath(ch.Path)
			if rerr != nil {
				return nil, "", rerr
			}
			localTmp = filepath.Join(entry.Abs, filepath.FromSlash(rel))
			info, serr := os.Stat(localTmp)
			if serr != nil || !info.Mode().IsRegular() {
				return nil, "", errf(1, "local file %q changed while diffing", ch.Path).
					withHint("re-run the command; a file that moves during the diff cannot produce a stable patch")
			}
		}

		// --no-textconv: a .gitattributes driver must not rewrite document
		// bytes (or run a command) while the user only asked to see a diff.
		out, derr := rc.Store.Run(ctx, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-index", baseTmp, localTmp)
		if derr != nil {
			if ctx.Err() != nil {
				return nil, "", wrapErr(ctx.Err(), 1)
			}
			var ge *gitstore.Error
			if !errors.As(derr, &ge) || ge.Code != 1 {
				wrapped := wrapErr(derr, 1)
				if ae, ok := wrapped.(*Error); ok && ae.Hint == "" {
					_ = ae.withHint("check that the docs directory and the local mirror are readable, then retry")
				}
				return nil, "", wrapped
			}
			// Exit code 1 means "the files differ"; stdout is the patch.
		}

		block := rewriteDiffBlock(out, ch)
		parsed := parseDiff(block)
		if len(parsed) != 1 {
			return nil, "", errf(1, "patch for %q did not produce exactly one block", ch.Path)
		}
		file := parsed[0]
		file.Path = ch.Path
		file.Status = ch.Status
		files = append(files, file)
		raw.WriteString(block)
	}
	return files, raw.String(), nil
}

// rewriteDiffBlock replaces the scratch file names git prints with the project
// path and restores the new/deleted mode line that the empty stand-in hides.
// Only header lines before the first hunk (or the binary line) are touched, so
// a deleted content line that itself reads like a header cannot be rewritten.
// git's old/new mode lines are dropped: the scratch files are 0600, so those
// modes describe the temp files rather than the revisions. Content identity
// decides M, so a mode-only difference is not reported; added and deleted
// files still carry the mode the tree recorded.
func rewriteDiffBlock(raw string, ch diffChange) string {
	var out strings.Builder
	out.WriteString("diff --git a/" + ch.Path + " b/" + ch.Path + "\n")
	switch {
	case !ch.HasBase:
		fmt.Fprintf(&out, "new file mode %06o\n", ch.Local.Mode)
	case !ch.HasLocal:
		fmt.Fprintf(&out, "deleted file mode %06o\n", ch.Base.Mode)
	}

	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	inBody := false
	for i, line := range lines {
		if i == 0 {
			continue // git's own header names the scratch files
		}
		switch {
		case inBody:
			out.WriteString(line + "\n")
		case strings.HasPrefix(line, "@@ "):
			inBody = true
			out.WriteString(line + "\n")
		case strings.HasPrefix(line, "old mode "), strings.HasPrefix(line, "new mode "):
			// Replaced above with the modes recorded in the revisions.
		case strings.HasPrefix(line, "--- "):
			out.WriteString("--- " + diffOperand("a", ch.Path, ch.HasBase) + headerTab(ch.Path) + "\n")
		case strings.HasPrefix(line, "+++ "):
			out.WriteString("+++ " + diffOperand("b", ch.Path, ch.HasLocal) + headerTab(ch.Path) + "\n")
		case strings.HasPrefix(line, "Binary files "):
			inBody = true
			out.WriteString("Binary files " + diffOperand("a", ch.Path, ch.HasBase) +
				" and " + diffOperand("b", ch.Path, ch.HasLocal) + " differ\n")
		default:
			out.WriteString(line + "\n")
		}
	}
	return out.String()
}

// diffOperand names one side of the rewritten patch. A missing side reads as
// /dev/null, exactly as it does in a real git diff.
func diffOperand(prefix, path string, exists bool) string {
	if !exists {
		return "/dev/null"
	}
	return prefix + "/" + path
}

// headerTab separates a path containing spaces from the rest of a "---"/"+++"
// line, because git's own output does the same.
func headerTab(path string) string {
	if strings.ContainsRune(path, ' ') {
		return "\t"
	}
	return ""
}

// diffBase resolves the base revision with the same ladder as Restore:
// the positional commit, then --from, then the fetched remote tip.
func diffBase(ctx context.Context, rc *RepoContext, opts DiffOptions) (string, error) {
	rev := opts.Commit
	if rev == "" {
		rev = opts.From
	}
	if rev == "" {
		tip, rerr := rc.Store.RefTip(ctx, gitstore.RemoteBranch(rc.Config.Branch))
		if rerr != nil {
			return "", wrapErr(rerr, 1)
		}
		if tip == "" {
			return "", errf(codeUsage, "no fetched remote tip to diff against").
				withHint("pass a commit shown by \"doc-connector log\", or run status once while online")
		}
		rev = tip
	}
	commit, cerr := rc.Store.Run(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if cerr != nil || strings.TrimSpace(commit) == "" {
		return "", errf(codeUsage, "unknown revision %q", rev).
			withHint("list commits with \"doc-connector log\" and pass a revision shown there")
	}
	return strings.TrimSpace(commit), nil
}

// diffTarget resolves the latest synced revision reported in To: the mirror
// tip, then the last synced commit recorded in state.
func diffTarget(ctx context.Context, rc *RepoContext) (string, error) {
	commit := ""
	tip, rerr := rc.Store.RefTip(ctx, gitstore.RemoteBranch(rc.Config.Branch))
	if rerr != nil {
		return "", wrapErr(rerr, 1)
	}
	if tip != "" {
		commit = tip
	} else if rc.State.SyncedCommit != "" {
		commit = rc.State.SyncedCommit
	}
	if commit == "" {
		return "", errf(codeUsage, "no synced revision to compare against").
			withHint("run status or pull once so the local mirror holds a revision")
	}
	oid, cerr := rc.Store.Run(ctx, "rev-parse", "--verify", "--quiet", commit+"^{commit}")
	if cerr != nil || strings.TrimSpace(oid) == "" {
		return "", errf(codeUsage, "revision %q is missing from the local mirror", commit).
			withHint("run pull once while online to refill the mirror")
	}
	return strings.TrimSpace(oid), nil
}

// parseDiff splits a unified patch into one entry per "diff --git" block.
func parseDiff(raw string) []DiffFile {
	files := []DiffFile{}
	if raw == "" {
		return files
	}
	lines := strings.Split(raw, "\n")
	start := -1
	for i, line := range lines {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		if start >= 0 {
			files = append(files, parseDiffBlock(lines[start:i]))
		}
		start = i
	}
	if start >= 0 {
		files = append(files, parseDiffBlock(lines[start:]))
	}
	return files
}

// parseDiffBlock reads one file's block: status markers, path, line counts and
// the text itself. Header lines are only read before the first hunk, so a
// content line that happens to start with "+++ " cannot rename the file.
func parseDiffBlock(lines []string) DiffFile {
	status := "M"
	path := ""
	header := ""
	binaryLine := ""
	inHunk := false
	for _, line := range lines {
		if inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "@@ "):
			inHunk = true
		case strings.HasPrefix(line, "diff --git "):
			header = line
		case strings.HasPrefix(line, "new file mode "):
			status = "A"
		case strings.HasPrefix(line, "deleted file mode "):
			status = "D"
		case strings.HasPrefix(line, "+++ "):
			if p := diffPath(line[len("+++ "):]); p != "" {
				path = p
			}
		case strings.HasPrefix(line, "--- "):
			if p := diffPath(line[len("--- "):]); p != "" && path == "" {
				path = p
			}
		case strings.HasPrefix(line, "rename to "):
			if p := diffPath(strings.TrimPrefix(line, "rename to ")); p != "" {
				path = p
			}
		case strings.HasPrefix(line, "copy to "):
			if p := diffPath(strings.TrimPrefix(line, "copy to ")); p != "" {
				path = p
			}
		case strings.HasPrefix(line, "Binary files "):
			binaryLine = line
		}
	}
	// Binary blocks carry no ---/+++ lines, so the header is the only source.
	if path == "" && header != "" {
		path = headerPath(header)
	}

	insertions, deletions := 0, 0
	inHunk = false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "@@ "):
			inHunk = true
		case !inHunk:
			// mode/index/---/+++ headers are not content
		case strings.HasPrefix(line, "+"):
			insertions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}

	text := strings.Join(lines, "\n")
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if binaryLine != "" {
		text = binaryLine + "\n"
		insertions, deletions = 0, 0
	}
	patch, truncated := truncatePatch(text)
	return DiffFile{
		Path:       path,
		Status:     status,
		Insertions: insertions,
		Deletions:  deletions,
		Patch:      patch,
		Truncated:  truncated,
	}
}

// diffPath turns one "---"/"+++" operand into a project-relative path. It
// returns "" for /dev/null.
func diffPath(field string) string {
	s := strings.TrimSuffix(field, "\r")
	if strings.HasPrefix(s, "\"") {
		if unquoted, err := strconv.Unquote(s); err == nil {
			s = unquoted
		}
	}
	// git appends a tab when the name contains a space, and other producers
	// append a timestamp after one.
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "a/")
	s = strings.TrimPrefix(s, "b/")
	if s == "/dev/null" {
		return ""
	}
	return s
}

// headerPath extracts the path from a "diff --git a/<p> b/<p>" header. It only
// accepts an unambiguous split, where both sides name the same path; renames
// supply their target through "rename to" instead.
func headerPath(line string) string {
	after, ok := strings.CutPrefix(strings.TrimPrefix(line, "diff --git "), "a/")
	if !ok {
		return ""
	}
	for start := 0; start < len(after); {
		i := strings.Index(after[start:], " b/")
		if i < 0 {
			return ""
		}
		i += start
		oldPath, newPath := after[:i], after[i+len(" b/"):]
		if oldPath == newPath {
			return diffPath("a/" + oldPath)
		}
		start = i + 1
	}
	return ""
}

// truncatePatch cuts s at maxPatchBytes without splitting a UTF-8 rune.
func truncatePatch(s string) (string, bool) {
	if len(s) <= maxPatchBytes {
		return s, false
	}
	cut := maxPatchBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}
