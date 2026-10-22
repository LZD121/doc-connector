package ignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDocsIgnore(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, DocsIgnoreFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDocIgnoreRules(t *testing.T) {
	dir := t.TempDir()
	writeDocsIgnore(t, dir, `# local scratch files

*.tmp
Secret.md
build/
drafts/
`)

	f := NewWithDocsIgnore(nil, []string{dir})
	if got := f.Warnings(); len(got) != 0 {
		t.Fatalf("warnings = %v", got)
	}
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"a.tmp", false, true},
		{"notes/B.TMP", false, true},
		{"secret.md", false, true},
		{"notes/SECRET.MD", false, true},
		{"build", true, true},
		{"build/x.md", false, true},
		{"a/build/x.md", false, true},
		{"notes/drafts", true, true},
		{"notes/drafts/plan.md", false, true},
		{DocsIgnoreFile, false, true},
		{"notes/" + DocsIgnoreFile, false, true},
		// Built-in junk still wins and cannot be removed by the file.
		{".DS_Store", false, true},
		{".git", true, true},
		{"keep.md", false, false},
		{"builder.md", false, false},
		{"tmp", false, false},
	}
	for _, c := range cases {
		if got := f.Match(c.rel, c.isDir); got != c.want {
			t.Errorf("Match(%q, %v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
}

func TestDocIgnoreUnsupportedSyntax(t *testing.T) {
	dir := t.TempDir()
	writeDocsIgnore(t, dir, "!keep.md\nlogs/**/debug.txt\n*.bak\n")

	f := NewWithDocsIgnore(nil, []string{dir})
	warnings := f.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one per file", warnings)
	}
	if !strings.Contains(warnings[0], filepath.Join(dir, DocsIgnoreFile)) ||
		!strings.Contains(warnings[0], "line 1") ||
		!strings.Contains(warnings[0], "line 2") {
		t.Errorf("warning does not name the file and both lines: %q", warnings[0])
	}
	if !strings.Contains(warnings[0], "not supported") {
		t.Errorf("warning does not explain the limitation: %q", warnings[0])
	}
	// Unsupported lines match literally: no negation, no ** recursion.
	if f.Match("keep.md", false) {
		t.Error("! negation was interpreted")
	}
	if !f.Match("!keep.md", false) {
		t.Error("literal !keep.md rule does not match its own name")
	}
	if f.Match("logs/a/b/debug.txt", false) {
		t.Error("** was interpreted as a wildcard path")
	}
	if !f.Match("a.bak", false) {
		t.Error("supported basename rule lost")
	}
}

func TestDocIgnoreAbsentAndEmpty(t *testing.T) {
	f := NewWithDocsIgnore([]string{"scratch/"}, []string{t.TempDir(), ""})
	if got := f.Warnings(); len(got) != 0 {
		t.Errorf("warnings = %v", got)
	}
	if !f.Match("scratch", true) {
		t.Error("config extra rule lost")
	}
	if f.Match("keep.md", false) {
		t.Error("absent .docignore changed the rules")
	}
}

func TestFilterMatch(t *testing.T) {
	f := New([]string{"*.bak", "scratch/"})
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{".DS_Store", false, true},
		{"notes/.DS_Store", false, true},
		{"._resource", false, true},
		{"Thumbs.db", false, true},
		{"desktop.ini", false, true},
		{"notes/plan.md.theirs.laptop", false, true},
		{"notes/plan.md.theirs.laptop.2", false, true},
		{".git", true, true},
		{"nested/.git", true, true},
		{".doc-connector-conflicts", true, true},
		{"notes/a.bak", false, false}, // extra rules are basename-only in v1
		{"scratch", true, true},
		{"notes/real.md", false, false},
		{"a/b/c.txt", false, false},
	}
	for _, c := range cases {
		if got := f.Match(c.rel, c.isDir); got != c.want {
			t.Errorf("Match(%q, %v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
}
