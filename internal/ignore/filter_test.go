package ignore

import "testing"

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
