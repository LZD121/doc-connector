package app

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LZD121/doc-connector/internal/gitstore"
)

// DiffOptions selects the revision to start from and an optional path.
type DiffOptions struct {
	Commit string
	From   string
	Path   string
}

// DiffFile is one file in a historical diff. Path is relative to the project
// prefix, so it reads the same way as every other user-facing path.
type DiffFile struct {
	Path       string
	Status     string // A (added), D (deleted) or M (modified)
	Insertions int
	Deletions  int
	Patch      string
	Truncated  bool
}

// DiffResult is one historical diff of the project prefix.
//
// To is the revision the base is compared against. It is the mirror's
// remote-tracking tip (or the last synced commit when the ref is gone), which
// is the closest local stand-in for the docs directory: push and pull advance
// both together.
type DiffResult struct {
	From  string // full OID of the resolved base revision
	To    string // full OID of the revision compared against
	Path  string // user path filter, empty when the whole prefix is shown
	Files []DiffFile
	Raw   string // the unified patch, byte for byte
}

// maxPatchBytes bounds one DiffFile.Patch so a huge document cannot flood a
// JSON consumer. Raw always carries the complete patch.
const maxPatchBytes = 64 << 10

// Diff shows how one historical version of the project prefix differs from the
// currently synced revision. It runs entirely against the local mirror: no
// fetch, no ref or object writes, no state writes.
//
// The docs directory is an ordinary directory, not a git work tree, and the
// mirror is bare, so `git diff <tree>` (tree versus work tree) cannot run here.
// The command therefore diffs two <project_id> subtrees inside the mirror: the
// resolved base revision and the current target. Because both operands are the
// prefix subtree, patch paths are already project-relative and a path argument
// is a plain subtree path. When a revision has no <project_id> entry, the empty
// tree stands in for it, so the whole prefix reads as added or deleted instead
// of failing with a bare "bad object".
func Diff(ctx context.Context, env Env, opts DiffOptions) (*DiffResult, error) {
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: false})
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	if opts.Path != "" {
		if err := validateUserPath(opts.Path, rc); err != nil {
			return nil, wrapErr(err, codeGuard)
		}
	}

	base, err := diffBase(ctx, rc, opts)
	if err != nil {
		return nil, err
	}
	target, err := diffTarget(ctx, rc)
	if err != nil {
		return nil, err
	}

	fromSide, err := projectSide(ctx, rc, base)
	if err != nil {
		return nil, err
	}
	toSide, err := projectSide(ctx, rc, target)
	if err != nil {
		return nil, err
	}

	// core.quotePath=false keeps non-ASCII names readable in the patch instead
	// of C-quoted octal escapes; the parser still unquotes when git must quote.
	args := []string{
		"-c", "core.quotePath=false",
		"diff", "--no-color", "--no-ext-diff", fromSide, toSide,
	}
	if opts.Path != "" {
		args = append(args, "--", opts.Path)
	}
	raw, derr := rc.Store.Run(ctx, args...)
	if derr != nil {
		return nil, wrapErr(derr, 1)
	}

	return &DiffResult{
		From:  base,
		To:    target,
		Path:  opts.Path,
		Files: parseDiff(raw),
		Raw:   raw,
	}, nil
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

// diffTarget resolves the revision to compare against: the mirror tip, then the
// last synced commit recorded in state.
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

// projectSide returns the tree-ish for one revision's project prefix, or the
// empty tree when that revision has no such entry.
func projectSide(ctx context.Context, rc *RepoContext, commit string) (string, error) {
	_, ok, err := rc.Store.TreeOf(ctx, commit, rc.Config.Project.ID)
	if err != nil {
		return "", wrapErr(err, 1)
	}
	if ok {
		return commit + ":" + rc.Config.Project.ID, nil
	}
	out, herr := rc.Store.Run(ctx, "hash-object", "-t", "tree", "--stdin")
	if herr != nil {
		return "", wrapErr(herr, 1)
	}
	// Computed rather than hard-coded: a sha256 project has a different empty
	// tree id, and hash-object adds no object to the mirror without -w.
	return strings.TrimSpace(out), nil
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
		case strings.HasPrefix(line, "@@ "):
			inHunk = true
		case inHunk:
			// hunk body; counts are taken in a second pass
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
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
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
