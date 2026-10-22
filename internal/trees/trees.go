package trees

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/LZD121/doc-connector/internal/fsx"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/state"
)

// Entry is one blob in a tree. Mode is the git mode (0o100644 or 0o100755).
type Entry struct {
	Mode uint32
	OID  string
}

// Tree maps a slash-separated path relative to the docs root to its entry.
type Tree map[string]Entry

// Error is a tree-shape failure. Symlinks and unexpected object types are
// invalid remote input, so they carry exit code 3.
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

// Equal compares mode and OID. Mode participates so chmod +x is a modification.
func Equal(a, b Entry) bool { return a.Mode == b.Mode && a.OID == b.OID }

func ptr(t Tree, p string) *Entry {
	if e, ok := t[p]; ok {
		return &e
	}
	return nil
}

// ReadTree reads <rev> recursively with ls-tree. rev is typically
// "<commit>:<project_id>"; a missing revision is not an error only when the
// caller already established that the prefix is absent.
func ReadTree(ctx context.Context, store *gitstore.Store, rev string) (Tree, error) {
	out, err := store.Run(ctx, "ls-tree", "-r", "-z", rev)
	if err != nil {
		return nil, err
	}
	tree, err := ParseLsTree(out)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(tree))
	for p := range tree {
		paths = append(paths, p)
	}
	if err := fsx.CheckCaseCollisions(paths); err != nil {
		return nil, err
	}
	return tree, nil
}

// NormalizeModes returns a copy of local where, for equal OIDs, the mode is
// taken from base (or remote). It is used on Windows, which cannot express the
// executable bit, so a chmod difference must not look like a modification.
func NormalizeModes(local, base, remote Tree) Tree {
	out := make(Tree, len(local))
	for p, e := range local {
		ref, ok := base[p]
		if !ok {
			ref, ok = remote[p]
		}
		if ok && ref.OID == e.OID && ref.Mode != e.Mode {
			e.Mode = ref.Mode
		}
		out[p] = e
	}
	return out
}

// ParseLsTree parses NUL-terminated `ls-tree -r -z` output.
func ParseLsTree(out string) (Tree, error) {
	tree := Tree{}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, &Error{Code: 3, Msg: fmt.Sprintf("malformed ls-tree record %q", rec)}
		}
		meta, name := rec[:tab], rec[tab+1:]
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, &Error{Code: 3, Msg: fmt.Sprintf("malformed ls-tree meta %q", meta)}
		}
		mode, perr := strconv.ParseUint(fields[0], 8, 32)
		if perr != nil {
			return nil, &Error{Code: 3, Msg: fmt.Sprintf("invalid mode %q for %s", fields[0], name), Err: perr}
		}
		if fields[1] != "blob" {
			return nil, &Error{
				Code: 3,
				Msg:  fmt.Sprintf("remote tree entry %q has type %q, not a file", name, fields[1]),
				Hint: "the remote prefix must contain only regular files",
			}
		}
		if mode == 0o120000 {
			return nil, &Error{
				Code: 3,
				Msg:  fmt.Sprintf("remote tree contains a symbolic link at %q", name),
				Hint: "remove the symlink from the private repository; doc-connector never follows links",
			}
		}
		if mode != 0o100644 && mode != 0o100755 {
			return nil, &Error{
				Code: 3,
				Msg:  fmt.Sprintf("remote tree entry %q has unsupported mode %o", name, mode),
			}
		}
		tree[name] = Entry{Mode: uint32(mode), OID: fields[2]}
	}
	return tree, nil
}

// LoadBaseline derives the baseline tree from the synced commit. The cached
// baseline_tree in state is advisory: when it disagrees with the commit, the
// commit wins and a warning is returned.
func LoadBaseline(ctx context.Context, store *gitstore.Store, st state.State, projectID string) (Tree, []string, error) {
	if st.SyncedCommit == "" {
		return Tree{}, nil, nil
	}
	oid, ok, err := store.TreeOf(ctx, st.SyncedCommit, projectID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return Tree{}, []string{"state.synced_commit has no project prefix; baseline treated as empty"}, nil
	}
	tree, err := ReadTree(ctx, store, st.SyncedCommit+":"+projectID)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	if st.BaselineTree != "" && st.BaselineTree != oid {
		warnings = append(warnings, "state.baseline_tree disagreed with the synced commit; using the commit")
	}
	return tree, warnings, nil
}
