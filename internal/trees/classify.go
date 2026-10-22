package trees

import (
	"fmt"
	"sort"
)

// Class is one row of the three-way table.
type Class int

const (
	Unchanged Class = iota
	LocalAdd
	RemoteAdd
	Converged
	AddAdd
	LocalMod
	RemoteMod
	BothModified
	BothSame
	LocalDelete
	DeleteModify
	RemoteDelete
	ModifyDelete
	BothDeleted
)

// Action is the classification of one path.
type Action struct {
	Path   string
	Class  Class
	Status string // A, M, D, C, =, or "" for unchanged
	Local  *Entry
	Base   *Entry
	Remote *Entry
}

// Summary aggregates direction of change.
type Summary struct {
	LocalChanges  int
	RemoteChanges int
	Conflicts     int
}

// String renders clean/ahead/behind/diverged.
func (s Summary) String() string {
	switch {
	case s.LocalChanges > 0 && s.RemoteChanges > 0:
		return "diverged"
	case s.LocalChanges > 0:
		return "ahead"
	case s.RemoteChanges > 0:
		return "behind"
	default:
		return "clean"
	}
}

// Classify implements the 15-row table from ARCHITECTURE.md. It is a pure
// function: status, pull and push all call it so they can never disagree.
func Classify(base, local, remote Tree) ([]Action, Summary) {
	seen := map[string]struct{}{}
	for p := range base {
		seen[p] = struct{}{}
	}
	for p := range local {
		seen[p] = struct{}{}
	}
	for p := range remote {
		seen[p] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	actions := make([]Action, 0, len(paths))
	var summary Summary
	for _, p := range paths {
		b, l, r := ptr(base, p), ptr(local, p), ptr(remote, p)
		a := Action{Path: p, Base: b, Local: l, Remote: r}
		a.Class, a.Status = row(b, l, r)
		switch a.Class {
		case LocalAdd, LocalMod, LocalDelete:
			summary.LocalChanges++
		case RemoteAdd, RemoteMod, RemoteDelete:
			summary.RemoteChanges++
		case AddAdd, BothModified, DeleteModify, ModifyDelete:
			summary.LocalChanges++
			summary.RemoteChanges++
			summary.Conflicts++
		case Unchanged, Converged, BothSame, BothDeleted:
			// No directional change.
		}
		actions = append(actions, a)
	}
	return actions, summary
}

func row(b, l, r *Entry) (Class, string) {
	if b == nil {
		switch {
		case l != nil && r == nil:
			return LocalAdd, "A"
		case l == nil && r != nil:
			return RemoteAdd, "A"
		case l != nil && r != nil && Equal(*l, *r):
			return Converged, "="
		case l != nil && r != nil:
			return AddAdd, "C"
		}
		return Unchanged, ""
	}
	switch {
	case l != nil && r != nil:
		switch {
		case Equal(*l, *b) && Equal(*r, *b):
			return Unchanged, ""
		case !Equal(*l, *b) && Equal(*r, *b):
			return LocalMod, "M"
		case Equal(*l, *b) && !Equal(*r, *b):
			return RemoteMod, "M"
		case Equal(*l, *r):
			return BothSame, "="
		default:
			return BothModified, "C"
		}
	case l == nil && r != nil:
		if Equal(*r, *b) {
			return LocalDelete, "D"
		}
		return DeleteModify, "C"
	case l != nil && r == nil:
		if Equal(*l, *b) {
			return RemoteDelete, "D"
		}
		return ModifyDelete, "C"
	default:
		return BothDeleted, ""
	}
}

// Clobber returns paths where the remote holds a version the local side has not
// seen. Push refuses to overwrite these. resolved paths are exempt (the user
// explicitly chose the local version).
func Clobber(base, local, remote Tree, resolved map[string]bool) []string {
	seen := map[string]struct{}{}
	for p := range base {
		seen[p] = struct{}{}
	}
	for p := range local {
		seen[p] = struct{}{}
	}
	for p := range remote {
		seen[p] = struct{}{}
	}
	paths := make([]string, 0)
	for p := range seen {
		if resolved[p] {
			continue
		}
		b, l, r := ptr(base, p), ptr(local, p), ptr(remote, p)
		if samePtr(r, b) {
			continue
		}
		if samePtr(l, r) {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func samePtr(a, b *Entry) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return Equal(*a, *b)
}

// describe is used by tests and diagnostics.
func (c Class) String() string {
	names := []string{
		"unchanged", "local-add", "remote-add", "converged", "add/add",
		"local-mod", "remote-mod", "both-modified", "both-same",
		"local-delete", "delete/modify", "remote-delete", "modify/delete",
		"both-deleted",
	}
	if int(c) < len(names) {
		return names[c]
	}
	return fmt.Sprintf("class(%d)", int(c))
}
