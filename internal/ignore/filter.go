// Package ignore implements the fixed, closed junk set that never enters a
// sync. It deliberately does not parse .gitignore or .docignore: a repository
// file must not change the synchronized set between machines.
package ignore

import (
	"path"
	"strings"
)

// Filter matches built-in junk plus optional extra basename rules.
type Filter struct {
	extra []string
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
	return false
}
