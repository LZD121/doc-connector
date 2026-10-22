package conflict

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/fsx"
)

func TestArtifactPathDedupe(t *testing.T) {
	dir := t.TempDir()
	root, err := fsx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	first, err := ArtifactPath(root, "notes/a.md", "laptop", 240)
	if err != nil {
		t.Fatal(err)
	}
	if first != "notes/a.md.theirs.laptop" {
		t.Fatalf("first = %q", first)
	}
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "a.md.theirs.laptop"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactPath(root, "notes/a.md", "laptop", 240)
	if err != nil {
		t.Fatal(err)
	}
	if second != "notes/a.md.theirs.laptop.2" {
		t.Errorf("second = %q", second)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "a.md.theirs.laptop.2"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := ArtifactPath(root, "notes/a.md", "laptop", 240)
	if err != nil {
		t.Fatal(err)
	}
	if third != "notes/a.md.theirs.laptop.3" {
		t.Errorf("third = %q", third)
	}
}

func TestArtifactPathFallbackWhenLong(t *testing.T) {
	dir := t.TempDir()
	root, err := fsx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	long := strings.Repeat("nested/", 30) + "a.md"
	got, err := ArtifactPath(root, long, "laptop", 80)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, ".doc-connector-conflicts/") {
		t.Errorf("fallback = %q", got)
	}
}
