package gitstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func plumbingRepo(t *testing.T) (string, *Store, *Runner) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "pid"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pid", "old.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other", "keep.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "seed")

	r := New(dir)
	return dir, NewStore(r, filepath.Join(dir, ".git")), r
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestBuildSubtreeAndSplice(t *testing.T) {
	ctx := context.Background()
	dir, store, _ := plumbingRepo(t)
	oids, err := store.HashPaths(ctx, dir, []string{"pid/old.md", "other/keep.md"}, true)
	if err != nil {
		t.Fatal(err)
	}
	entries := []IndexEntry{
		{Mode: 0o100644, OID: oids[0], Path: "notes/new.md"},
		{Mode: 0o100755, OID: oids[1], Path: "run.sh"},
	}
	sub, err := store.BuildSubtree(ctx, entries, filepath.Join(t.TempDir(), "idx"), false)
	if err != nil {
		t.Fatal(err)
	}
	listing := runGit(t, dir, "ls-tree", "-r", sub)
	if !strings.Contains(listing, "notes/new.md") || !strings.Contains(listing, "100755") {
		t.Errorf("subtree listing = %s", listing)
	}

	tip := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	root, err := store.SpliceRoot(ctx, tip, "pid", sub, true, false)
	if err != nil {
		t.Fatal(err)
	}
	rootListing := runGit(t, dir, "ls-tree", "-r", root)
	if !strings.Contains(rootListing, "pid/run.sh") || !strings.Contains(rootListing, "pid/notes/new.md") {
		t.Errorf("new subtree missing:\n%s", rootListing)
	}
	if strings.Contains(rootListing, "pid/old.md") {
		t.Errorf("old prefix entry survived:\n%s", rootListing)
	}
	if !strings.Contains(rootListing, "other/keep.md") {
		t.Errorf("other project entry lost:\n%s", rootListing)
	}

	// Empty subtree removes the prefix entirely.
	empty, err := store.BuildSubtree(ctx, nil, filepath.Join(t.TempDir(), "idx-empty"), false)
	if err != nil {
		t.Fatal(err)
	}
	root2, err := store.SpliceRoot(ctx, tip, "pid", empty, false, false)
	if err != nil {
		t.Fatal(err)
	}
	root2Listing := runGit(t, dir, "ls-tree", "-r", root2)
	if strings.Contains(root2Listing, "pid/") {
		t.Errorf("prefix not removed:\n%s", root2Listing)
	}
	if !strings.Contains(root2Listing, "other/keep.md") {
		t.Errorf("other project entry lost on removal:\n%s", root2Listing)
	}
}

func TestCommitAndAncestor(t *testing.T) {
	ctx := context.Background()
	_, store, r := plumbingRepo(t)
	tipOut, err := r.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tip := strings.TrimSpace(tipOut)
	tree, err := store.TreeID(ctx, tip)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := store.CommitTree(ctx, tree, tip, "child\n\nDoc-Connector-Project: abc\n", Signature{Name: "doc-connector", Email: "doc-connector@test"})
	if err != nil {
		t.Fatal(err)
	}
	if commit == "" || commit == tip {
		t.Fatalf("commit = %q", commit)
	}
	ok, err := store.Ancestor(ctx, tip, commit)
	if err != nil || !ok {
		t.Errorf("ancestor = %v, %v", ok, err)
	}
	ok, err = store.Ancestor(ctx, commit, tip)
	if err != nil || ok {
		t.Errorf("reverse ancestor = %v, %v", ok, err)
	}
	body, err := r.Run(ctx, "cat-file", "commit", commit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Doc-Connector-Project: abc") {
		t.Errorf("message lost:\n%s", body)
	}
}

func TestPushLeaseRejectionIsRace(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", "-q", "-b", "main", remoteDir)
	url := "file://" + remoteDir

	_, store, _ := plumbingRepo(t)
	tipOut, err := store.Runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tip := strings.TrimSpace(tipOut)
	tree, err := store.TreeID(ctx, tip)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CommitTree(ctx, tree, tip, "first\n", Signature{Name: "t", Email: "t@e"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Push(ctx, url, "main", first, ""); err != nil {
		t.Fatalf("first push: %v", err)
	}
	second, err := store.CommitTree(ctx, tree, first, "second\n", Signature{Name: "t", Email: "t@e"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Push(ctx, url, "main", second, first); err != nil {
		t.Fatalf("second push: %v", err)
	}

	// Stale lease: expecting `first` while the remote is at `second`.
	stale, err := store.CommitTree(ctx, tree, first, "stale\n", Signature{Name: "t", Email: "t@e"})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Push(ctx, url, "main", stale, first)
	if err == nil {
		t.Fatal("stale lease push succeeded")
	}
	ge, ok := err.(*Error)
	if !ok || ge.Kind != KindRace {
		t.Fatalf("error = %v (kind %v), want race", err, ge)
	}
}
