package fsx

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// CaseKey is the cross-platform comparison key: NFC normalization of the
// lowercase path. macOS stores NFD, Windows and Linux store NFC; folding both
// is what makes "same file?" answerable across machines.
func CaseKey(p string) string {
	return norm.NFC.String(strings.ToLower(p))
}

// CheckCaseCollisions rejects a path set whose keys are not unique. All paths in
// every colliding group are listed, sorted, so the user can fix them in one pass.
func CheckCaseCollisions(paths []string) error {
	groups := map[string][]string{}
	for _, p := range paths {
		k := CaseKey(p)
		groups[k] = append(groups[k], p)
	}
	var collisions []string
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		sort.Strings(group)
		collisions = append(collisions, strings.Join(group, " == "))
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Strings(collisions)
	return &Error{
		Msg:  fmt.Sprintf("path collisions after case/NFC folding: %s", strings.Join(collisions, "; ")),
		Hint: "rename the colliding files; they cannot coexist on case-insensitive or normalization-normalizing filesystems",
	}
}

// NonNFCPaths lists paths that are not already NFC (macOS NFD artifacts).
func NonNFCPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if norm.NFC.String(p) != p {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
