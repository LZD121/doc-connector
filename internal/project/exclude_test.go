package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureExclude(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "info", "exclude")

	changed, err := EnsureExclude(path, "private-docs")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("first write reported no change")
	}
	data, _ := os.ReadFile(path)
	want := "# doc-connector:begin\n/private-docs/\n# doc-connector:end\n"
	if string(data) != want {
		t.Fatalf("content = %q, want %q", data, want)
	}

	changed, err = EnsureExclude(path, "private-docs")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second write reported a change")
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(data) {
		t.Errorf("idempotent write changed bytes: %q", again)
	}

	// Existing content without a trailing newline is preserved exactly.
	if err := os.WriteFile(path, []byte("*.tmp\nbuild/"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureExclude(path, "private-docs"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !strings.HasPrefix(string(data), "*.tmp\nbuild/\n# doc-connector:begin\n") {
		t.Errorf("existing content not preserved: %q", data)
	}

	// A different managed block is replaced in place, surroundings intact.
	old := "before\n# doc-connector:begin\n/old-docs/\n# doc-connector:end\nafter\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureExclude(path, "private-docs"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	want = "before\n" + ExcludeBlock("private-docs") + "after\n"
	if string(data) != want {
		t.Errorf("replacement = %q, want %q", data, want)
	}

	// Spaces are escaped.
	if _, err := EnsureExclude(path, "my docs"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), `/my\ docs/`) {
		t.Errorf("space not escaped: %q", data)
	}

	// Unterminated block is refused rather than clobbered.
	if err := os.WriteFile(path, []byte("# doc-connector:begin\n/private-docs/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureExclude(path, "private-docs"); err == nil {
		t.Error("unterminated managed block was accepted")
	}
}

func TestEnsureExcludeLeavesGitignore(t *testing.T) {
	dir := t.TempDir()
	gitignore := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("/private-docs/\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(gitignore)

	exclude := filepath.Join(dir, ".git", "info", "exclude")
	if _, err := EnsureExclude(exclude, "private-docs"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(gitignore)
	if string(before) != string(after) {
		t.Errorf(".gitignore changed: %q -> %q", before, after)
	}
}
