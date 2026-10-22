package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/gitstore"
)

func isolateGit(t *testing.T) {
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

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func TestDeriveID(t *testing.T) {
	id, err := DeriveID("743c420a8e7b11c3382c49dfec71987cb7a6f710")
	if err != nil {
		t.Fatal(err)
	}
	if id != "743c420a8e7b" {
		t.Errorf("DeriveID = %s, want 743c420a8e7b", id)
	}
	// SHA-256 repositories have 64-hex roots; the first 12 chars still work.
	long := strings.Repeat("Ab", 32)
	id, err = DeriveID(long)
	if err != nil {
		t.Fatal(err)
	}
	if id != strings.ToLower(long)[:12] {
		t.Errorf("DeriveID(sha256) = %s", id)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("a", 39), strings.Repeat("z", 40)} {
		if _, err := DeriveID(bad); err == nil {
			t.Errorf("DeriveID(%q) accepted invalid input", bad)
		}
	}
}

func TestDiscover(t *testing.T) {
	isolateGit(t)
	dir := newRepo(t)
	sub := filepath.Join(dir, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	r := gitstore.New(sub)
	repo, err := Discover(context.Background(), r, sub)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(dir)
	gotRoot, _ := filepath.EvalSymlinks(repo.WorkTree)
	if gotRoot != wantRoot {
		t.Errorf("WorkTree = %q, want %q", gotRoot, wantRoot)
	}
	if repo.Shallow {
		t.Error("fresh repo reported shallow")
	}
	if repo.ObjectFormat != "sha1" {
		t.Errorf("ObjectFormat = %q, want sha1", repo.ObjectFormat)
	}
}

func TestDiscoverRejectsBare(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	git(t, dir, "init", "--bare", "-q")
	r := gitstore.New(dir)
	_, err := Discover(context.Background(), r, dir)
	if err == nil {
		t.Fatal("expected failure in bare repository")
	}
	if code := err.(*Error).ExitCode(); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestDiscoverRejectsLinkedWorktree(t *testing.T) {
	isolateGit(t)
	dir := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", "-b", "side", wt)

	r := gitstore.New(wt)
	_, err := Discover(context.Background(), r, wt)
	if err == nil {
		t.Fatal("expected linked worktree rejection")
	}
	pe := err.(*Error)
	if pe.ExitCode() != 3 {
		t.Errorf("exit code = %d, want 3", pe.ExitCode())
	}
	if !strings.Contains(pe.Error(), "linked worktree") {
		t.Errorf("message = %q", pe.Error())
	}
}

func TestRootCommitsEmptyRepo(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	r := gitstore.New(dir)
	repo, err := Discover(context.Background(), r, dir)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := RootCommits(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("RootCommits: %v", err)
	}
	if len(roots) != 0 {
		t.Errorf("roots = %v, want none", roots)
	}
}

func TestDeriveRejectsUnbornAndShallow(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	r := gitstore.New(dir)
	repo, err := Discover(context.Background(), r, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Derive(context.Background(), r, repo); err == nil {
		t.Fatal("expected unborn repository rejection")
	} else if !strings.Contains(err.Error(), "no commits") {
		t.Errorf("message = %q", err.Error())
	}

	// Shallow clone of a repository with two commits.
	src := newRepo(t)
	if err := os.WriteFile(filepath.Join(src, "second.txt"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "second.txt")
	git(t, src, "commit", "-q", "-m", "second")
	clone := filepath.Join(t.TempDir(), "shallow")
	git(t, filepath.Dir(clone), "clone", "-q", "--depth", "1", "file://"+src, clone)

	rr := gitstore.New(clone)
	shallowRepo, err := Discover(context.Background(), rr, clone)
	if err != nil {
		t.Fatal(err)
	}
	if !shallowRepo.Shallow {
		t.Fatal("clone reported not shallow")
	}
	if _, err := Derive(context.Background(), rr, shallowRepo); err == nil {
		t.Fatal("expected shallow rejection")
	} else if !strings.Contains(err.Error(), "shallow") {
		t.Errorf("message = %q", err.Error())
	}
	// An explicit id is the documented escape hatch.
	id, err := Resolve(context.Background(), rr, shallowRepo, "abcdefabcdef", false)
	if err != nil {
		t.Fatalf("explicit id on shallow rejected: %v", err)
	}
	if id.ID != "abcdefabcdef" || id.Source != "explicit" {
		t.Errorf("identity = %+v", id)
	}
}

func TestDeriveRejectsMultipleRoots(t *testing.T) {
	isolateGit(t)
	dir := newRepo(t)
	git(t, dir, "checkout", "-q", "--orphan", "other")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "second root")
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "merge", "-q", "--allow-unrelated-histories", "--no-edit", "other")

	r := gitstore.New(dir)
	repo, err := Discover(context.Background(), r, dir)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := RootCommits(context.Background(), r, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("roots = %v, want 2", roots)
	}
	_, err = Derive(context.Background(), r, repo)
	if err == nil {
		t.Fatal("expected multiple-root rejection")
	}
	if !strings.Contains(err.Error(), "root commits") {
		t.Errorf("message = %q", err.Error())
	}
	for _, root := range roots {
		if !strings.Contains(err.Error(), root) {
			t.Errorf("message omits root %s: %q", root, err.Error())
		}
	}
}

func TestResolveExplicitMismatch(t *testing.T) {
	isolateGit(t)
	dir := newRepo(t)
	r := gitstore.New(dir)
	repo, err := Discover(context.Background(), r, dir)
	if err != nil {
		t.Fatal(err)
	}

	derived, err := Derive(context.Background(), r, repo)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := "0123456789ab"
	if mismatch == derived.ID {
		mismatch = "abcdefabcdef"
	}

	if _, err := Resolve(context.Background(), r, repo, mismatch, false); err == nil {
		t.Fatal("expected mismatch rejection without --force")
	} else if !strings.Contains(err.Error(), derived.ID) || !strings.Contains(err.Error(), mismatch) {
		t.Errorf("message must name both ids: %q", err.Error())
	}

	id, err := Resolve(context.Background(), r, repo, mismatch, true)
	if err != nil {
		t.Fatalf("force: %v", err)
	}
	if id.ID != mismatch || id.Source != "explicit" {
		t.Errorf("identity = %+v", id)
	}

	// Malformed explicit ids fail regardless of force.
	if _, err := Resolve(context.Background(), r, repo, "ABCDEFABCDEF", true); err == nil {
		t.Fatal("uppercase id accepted")
	}
}
