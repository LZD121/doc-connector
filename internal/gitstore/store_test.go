package gitstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func storeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func emptyRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	storeGit(t, t.TempDir(), "init", "--bare", "-q", "-b", "main", dir)
	return "file://" + dir
}

func seededRemote(t *testing.T, prefix string) (url, tip string) {
	t.Helper()
	isolate(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	storeGit(t, t.TempDir(), "init", "--bare", "-q", "-b", "main", bare)
	work := t.TempDir()
	storeGit(t, work, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(work, prefix, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, prefix, "notes", "a.md"), []byte("remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	storeGit(t, work, "add", ".")
	storeGit(t, work, "commit", "-q", "-m", "seed")
	storeGit(t, work, "remote", "add", "origin", "file://"+bare)
	storeGit(t, work, "push", "-q", "origin", "main")
	tip = strings.TrimSpace(storeGit(t, work, "rev-parse", "HEAD"))
	return "file://" + bare, tip
}

func TestDetectBranch(t *testing.T) {
	isolate(t)
	ctx := context.Background()
	r := New(t.TempDir())

	url := emptyRemote(t)
	branch, empty, err := DetectBranch(ctx, r, url)
	if err != nil {
		t.Fatalf("empty remote: %v", err)
	}
	if !empty || branch != "" {
		t.Errorf("empty remote: empty=%v branch=%q", empty, branch)
	}

	url, _ = seededRemote(t, "abc")
	branch, empty, err = DetectBranch(ctx, r, url)
	if err != nil {
		t.Fatalf("seeded remote: %v", err)
	}
	if empty || branch != "main" {
		t.Errorf("seeded remote: empty=%v branch=%q", empty, branch)
	}

	_, _, err = DetectBranch(ctx, r, "file://"+filepath.Join(t.TempDir(), "missing.git"))
	if err == nil {
		t.Fatal("missing remote accepted")
	}
	ge, ok := err.(*Error)
	if !ok || ge.ExitCode() != 4 {
		t.Errorf("missing remote error = %v (exit %v)", err, ge)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	isolate(t)
	ctx := context.Background()
	pid := "abcdefabcdef"
	url, tip := seededRemote(t, pid)

	storeDir := filepath.Join(t.TempDir(), "store.git")
	r := New(t.TempDir())
	store := NewStore(r, storeDir)
	if err := store.EnsureBare(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := store.EnsureBare(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if err := store.Fetch(ctx, url); err != nil {
		t.Fatal(err)
	}
	got, err := store.RefTip(ctx, RemoteBranch("main"))
	if err != nil {
		t.Fatal(err)
	}
	if got != tip {
		t.Errorf("tip = %q, want %q", got, tip)
	}
	tree, ok, err := store.TreeOf(ctx, got, pid)
	if err != nil || !ok || tree == "" {
		t.Errorf("TreeOf: tree=%q ok=%v err=%v", tree, ok, err)
	}
	if _, ok, _ := store.TreeOf(ctx, got, "000000000000"); ok {
		t.Error("TreeOf found a missing prefix")
	}
}

func TestStoreFetchEmptyRemote(t *testing.T) {
	isolate(t)
	ctx := context.Background()
	url := emptyRemote(t)
	r := New(t.TempDir())
	store := NewStore(r, filepath.Join(t.TempDir(), "store.git"))
	if err := store.EnsureBare(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if err := store.Fetch(ctx, url); err != nil {
		t.Fatalf("fetch on empty remote: %v", err)
	}
	tip, err := store.RefTip(ctx, RemoteBranch("main"))
	if err != nil || tip != "" {
		t.Errorf("tip = %q err = %v", tip, err)
	}
}
