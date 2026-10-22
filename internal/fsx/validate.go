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

var reservedNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"COM¹": {}, "COM²": {}, "COM³": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
	"LPT¹": {}, "LPT²": {}, "LPT³": {},
}

// ValidateSyncPath applies every rule a path must satisfy to enter or leave a
// synchronized tree, including Windows reserved names and the total length
// budget. budget is the remaining characters after the docs prefix.
func ValidateSyncPath(p string, budget int) error {
	if err := ValidateRel(p); err != nil {
		return err
	}
	if budget > 0 && len(p) > budget {
		return &Error{
			Msg:  fmt.Sprintf("path %q is %d characters, above the %d character budget", p, len(p), budget),
			Hint: "rename or move the file; Windows cannot check out longer paths",
		}
	}
	for _, part := range strings.Split(p, "/") {
		name := part
		if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
		if _, bad := reservedNames[strings.ToUpper(name)]; bad {
			return &Error{
				Msg:  fmt.Sprintf("path %q uses the Windows reserved name %q", p, part),
				Hint: "rename the file; reserved device names cannot exist on Windows",
			}
		}
		if strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
			return &Error{
				Msg:  fmt.Sprintf("path %q has a component ending in a space or dot", p),
				Hint: "rename the file; Windows silently strips these characters",
			}
		}
	}
	return nil
}
