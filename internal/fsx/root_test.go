package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRel(t *testing.T) {
	valid := []string{"a.md", "notes/a.md", "notes/deep/2026-10-22.txt", "UPPER.MD"}
	for _, p := range valid {
		if err := ValidateRel(p); err != nil {
			t.Errorf("ValidateRel(%q) rejected: %v", p, err)
		}
	}
	invalid := []string{"", ".", "..", "../escape", "/abs", "a//b", "a/", "a\\b", "a/.git/config", "a\x00b", "C:/windows"}
	for _, p := range invalid {
		if err := ValidateRel(p); err == nil {
			t.Errorf("ValidateRel(%q) accepted", p)
		}
	}
}

func TestRootRoundTrip(t *testing.T) {
	dir := t.TempDir()
	root, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.MkdirAll("notes", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("notes/a.md", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := root.ReadFile("notes/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q", data)
	}
	if err := root.WriteFile("../escape", []byte("x"), 0o644); err == nil {
		t.Fatal("escape accepted")
	}
}

func TestOpenRejectsSymlinkRoot(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "docs-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Open(link)
	if err == nil {
		t.Fatal("symlink root accepted")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("message = %q", err.Error())
	}
}
