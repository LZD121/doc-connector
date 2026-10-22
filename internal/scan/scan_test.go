package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/ignore"
)

func setupStore(t *testing.T) *gitstore.Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	r := gitstore.New(dir)
	return gitstore.NewStore(r, filepath.Join(dir, ".git"))
}

func TestScanContentIdentity(t *testing.T) {
	store := setupStore(t)
	docs := t.TempDir()
	write := func(rel, content string, mode os.FileMode) {
		p := filepath.Join(docs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "hello", 0o644)
	write("notes/b.md", "world", 0o644)
	write(".DS_Store", "junk", 0o644)
	write("plan.md.theirs.laptop", "artifact", 0o644)
	write("script.sh", "#!/bin/sh\n", 0o755)

	first, err := Scan(context.Background(), store, docs, Options{Filter: ignore.New(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tree) != 3 {
		t.Fatalf("tree = %v", first.Tree)
	}
	if first.Ignored != 2 {
		t.Errorf("ignored = %d, want 2", first.Ignored)
	}
	if runtime.GOOS != "windows" && first.Tree["script.sh"].Mode != 0o100755 {
		t.Errorf("executable mode = %o", first.Tree["script.sh"].Mode)
	}

	// touch: mtime changes, OID must not.
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(docs, "a.md"), later, later); err != nil {
		t.Fatal(err)
	}
	second, err := Scan(context.Background(), store, docs, Options{Filter: ignore.New(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if second.Tree["a.md"] != first.Tree["a.md"] {
		t.Errorf("touch changed the OID")
	}

	// Same-size edit: OID must change.
	write("a.md", "HELLO", 0o644)
	third, err := Scan(context.Background(), store, docs, Options{Filter: ignore.New(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if third.Tree["a.md"] == first.Tree["a.md"] {
		t.Errorf("same-size edit was missed")
	}
}

func TestScanSkipsSymlink(t *testing.T) {
	store := setupStore(t)
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "real.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.md", filepath.Join(docs, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	res, err := Scan(context.Background(), store, docs, Options{Filter: ignore.New(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Tree["link.md"]; ok {
		t.Error("symlink entered the tree")
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "link.md" {
		t.Errorf("skipped = %v", res.Skipped)
	}
}

func TestScanSizeLimit(t *testing.T) {
	store := setupStore(t)
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "big.bin"), []byte(strings.Repeat("x", 2048)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Scan(context.Background(), store, docs, Options{Filter: ignore.New(nil), MaxBytes: 1024})
	if err == nil {
		t.Fatal("oversized file accepted")
	}
}
