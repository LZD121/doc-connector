// Package ignore implements the built-in junk set that never enters a sync,
// plus the optional per-docs-directory .docignore file.
//
// The .docignore grammar is deliberately tiny: "#" comments, blank lines, a
// basename pattern (case-insensitive, "*" allowed) and a directory name ending
// in "/". Rules are additive: they can hide more than the built-in set but can
// never resurrect a path the built-in set rejects. "!" negation and "**" are
// not supported; such lines are matched literally and reported once per file.
package ignore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DocsIgnoreFile is the rule file read from each docs directory. It is itself
// always ignored: a rule file must never enter the synchronized tree.
const DocsIgnoreFile = ".docignore"

// Filter matches built-in junk plus optional extra basename rules and the
// rules of the .docignore files loaded from the docs directories.
type Filter struct {
	extra []string
	// base holds .docignore basename patterns, lowercased; "*" is a wildcard.
	base []string
	// dirs holds .docignore directory names, lowercased, matched against any
	// path component.
	dirs []string
	// exact and exactDirs hold patterns with unsupported syntax, matched
	// literally so a stray rule can never turn into a surprise wildcard.
	exact     []string
	exactDirs []string
	warnings  []string
}

var junkBasenames = map[string]struct{}{
	".ds_store":       {},
	"thumbs.db":       {},
	"ehthumbs.db":     {},
	"desktop.ini":     {},
	".localized":      {},
	"$recycle.bin":    {},
	".spotlight-v100": {},
	".trashes":        {},
	".docignore":      {},
}

var junkDirs = map[string]struct{}{
	".git":                     {},
	".svn":                     {},
	".doc-connector-conflicts": {},
}

// New builds a filter; extra entries are matched case-insensitively against the
// basename. An entry ending in "/" matches a directory name at any depth.
func New(extra []string) *Filter {
	clean := make([]string, 0, len(extra))
	for _, rule := range extra {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" {
			continue
		}
		clean = append(clean, rule)
	}
	return &Filter{extra: clean}
}

// NewWithDocsIgnore builds a filter and merges the .docignore rules found in
// each docs directory. An absent or unreadable file leaves the built-in set in
// place; unreadable files other than "not found" are reported as warnings.
func NewWithDocsIgnore(extra []string, docsDirs []string) *Filter {
	f := New(extra)
	for _, dir := range docsDirs {
		f.loadDocsIgnore(dir)
	}
	return f
}

// Warnings reports .docignore problems: unsupported syntax that was matched
// literally and rule files that could not be read. The caller surfaces them on
// stderr.
func (f *Filter) Warnings() []string {
	if f == nil {
		return nil
	}
	return f.warnings
}

func (f *Filter) loadDocsIgnore(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	file := filepath.Join(dir, DocsIgnoreFile)
	data, err := os.ReadFile(file)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			f.warnings = append(f.warnings, fmt.Sprintf("%s: cannot read ignore rules: %v; only the built-in rules apply", file, err))
		}
		return
	}
	var unsupported []string
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!") || strings.Contains(line, "**") {
			unsupported = append(unsupported, fmt.Sprintf("line %d", i+1))
			f.addLiteral(line)
			continue
		}
		f.addRule(line)
	}
	if len(unsupported) > 0 {
		f.warnings = append(f.warnings, fmt.Sprintf(
			"%s: %s: \"!\" negation and \"**\" are not supported; those lines are matched literally",
			file, strings.Join(unsupported, ", ")))
	}
}

// addRule appends one supported .docignore line.
func (f *Filter) addRule(line string) {
	lower := strings.ToLower(line)
	if strings.HasSuffix(lower, "/") {
		name := strings.TrimSuffix(lower, "/")
		if name == "" {
			return
		}
		f.dirs = append(f.dirs, name)
		return
	}
	f.base = append(f.base, lower)
}

// addLiteral appends one line with unsupported syntax. No character is special:
// the rule only matches a path component with exactly that name.
func (f *Filter) addLiteral(line string) {
	lower := strings.ToLower(line)
	if strings.HasSuffix(lower, "/") {
		name := strings.TrimSuffix(lower, "/")
		if name == "" {
			return
		}
		f.exactDirs = append(f.exactDirs, name)
		return
	}
	f.exact = append(f.exact, lower)
}

// Match reports whether rel (slash-separated, relative to the docs root) is
// ignored. isDir skips known junk directories early.
func (f *Filter) Match(rel string, isDir bool) bool {
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || rel == "." {
		return false
	}
	base := strings.ToLower(path.Base(rel))
	lowerRel := strings.ToLower(rel)

	for _, part := range strings.Split(lowerRel, "/") {
		if _, ok := junkDirs[part]; ok {
			return true
		}
	}
	if p := strings.ToLower(path.Dir(rel)); p == ".doc-connector-conflicts" || strings.HasSuffix(p, "/.doc-connector-conflicts") {
		return true
	}
	if strings.Contains(base, ".theirs.") {
		return true
	}
	if strings.HasPrefix(base, "._") {
		return true
	}
	if _, ok := junkBasenames[base]; ok {
		return true
	}
	for _, rule := range f.extra {
		if strings.HasSuffix(rule, "/") {
			if isDir && base == strings.TrimSuffix(rule, "/") {
				return true
			}
			continue
		}
		if base == rule {
			return true
		}
	}

	// .docignore rules: additive only, so the built-in set above always wins.
	for _, name := range f.exact {
		if base == name {
			return true
		}
	}
	for _, pattern := range f.base {
		if matchName(pattern, base) {
			return true
		}
	}
	if len(f.dirs) > 0 || len(f.exactDirs) > 0 {
		for _, part := range strings.Split(lowerRel, "/") {
			for _, name := range f.exactDirs {
				if part == name {
					return true
				}
			}
			for _, name := range f.dirs {
				if part == name {
					return true
				}
			}
		}
	}
	return false
}

// matchName matches a lowercased basename pattern against a lowercased
// basename. "*" matches any run of characters; every other byte is literal, so
// there is no glob engine and no escaping syntax to get wrong.
func matchName(pattern, name string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == name
	}
	if pattern == "" {
		return name == ""
	}
	if pattern[0] == '*' {
		rest := pattern[1:]
		for i := 0; i <= len(name); i++ {
			if matchName(rest, name[i:]) {
				return true
			}
		}
		return false
	}
	if name == "" || pattern[0] != name[0] {
		return false
	}
	return matchName(pattern[1:], name[1:])
}
