package gitstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func blobStore(t *testing.T) (*Store, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	r := New(dir)
	return NewStore(r, filepath.Join(dir, ".git")), dir
}

func TestWriteBlobs(t *testing.T) {
	store, work := blobStore(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(work, "one.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := []byte{0, 1, 2, 0xff, 0x00, 'x'}
	if err := os.WriteFile(filepath.Join(work, "two.bin"), bin, 0o644); err != nil {
		t.Fatal(err)
	}
	oids, err := store.HashPaths(ctx, work, []string{"one.txt", "two.bin"}, true)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "blobs")
	paths, err := store.WriteBlobs(ctx, oids, dest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths[oids[0]])
	if err != nil || string(data) != "hello" {
		t.Errorf("one = %q, %v", data, err)
	}
	got, err := os.ReadFile(paths[oids[1]])
	if err != nil || string(got) != string(bin) {
		t.Errorf("binary mismatch: %v", err)
	}

	if _, err := store.WriteBlobs(ctx, []string{"0000000000000000000000000000000000000000"}, dest); err == nil {
		t.Error("missing object accepted")
	}
}
