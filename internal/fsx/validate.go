package fsx

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateRel applies syntactic path rules shared by scan, pull and restore.
// Platform-specific rules (reserved names, length, case folding) are layered on
// in later phases; traversal safety is mandatory from day one.
func ValidateRel(p string) error {
	if p == "" {
		return &Error{Msg: "empty docs path"}
	}
	if p == "." {
		return &Error{Msg: "docs path must name a file, not the root directory"}
	}
	if !filepath.IsLocal(p) {
		return &Error{
			Msg:  fmt.Sprintf("docs path %q escapes the docs directory", p),
			Hint: "only relative paths inside docs_dir are allowed",
		}
	}
	if strings.ContainsRune(p, '\\') {
		return &Error{
			Msg:  fmt.Sprintf("docs path %q contains a backslash", p),
			Hint: "repository paths always use forward slashes",
		}
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, "//") {
		return &Error{Msg: fmt.Sprintf("docs path %q has an invalid separator layout", p)}
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return &Error{Msg: fmt.Sprintf("docs path %q contains a control character", p)}
		}
		if strings.ContainsRune(`<>:"|?*`, r) {
			return &Error{
				Msg:  fmt.Sprintf("docs path %q contains the character %q, which is invalid on Windows", p, r),
				Hint: "repository paths must be valid on every platform that syncs them",
			}
		}
	}
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "":
			return &Error{Msg: fmt.Sprintf("docs path %q has an empty component", p)}
		case ".", "..":
			return &Error{Msg: fmt.Sprintf("docs path %q has a %q component", p, part)}
		case ".git":
			return &Error{
				Msg:  fmt.Sprintf("docs path %q contains a .git component", p),
				Hint: "nested git repositories are never synchronized",
			}
		}
	}
	return nil
}
