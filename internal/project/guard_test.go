package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/gitstore"
)

func guardRepo(t *testing.T) (string, *gitstore.Runner, *Repo, string) {
	t.Helper()
	isolateGit(t)
	dir := newRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "private-docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := gitstore.New(dir)
	repo, err := Discover(context.Background(), r, dir)
	if err != nil {
		t.Fatal(err)
	}
	exclude := ExcludePath(repo.CommonDir)
	return dir, r, repo, exclude
}

func TestGuardUntracked(t *testing.T) {
	dir, r, _, _ := guardRepo(t)
	if err := GuardUntracked(context.Background(), r, "private-docs"); err != nil {
		t.Fatalf("untracked docs rejected: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "private-docs", "secret.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-f", "private-docs/secret.md")
	err := GuardUntracked(context.Background(), r, "private-docs")
	if err == nil {
		t.Fatal("tracked docs accepted")
	}
	pe := err.(*Error)
	if pe.ExitCode() != 3 || !strings.Contains(pe.Error(), "secret.md") {
		t.Errorf("error = %v (code %d)", pe, pe.ExitCode())
	}
}

func TestGuardIgnoredSource(t *testing.T) {
	dir, r, _, exclude := guardRepo(t)
	ctx := context.Background()

	if err := GuardIgnored(ctx, r, "private-docs", exclude); err == nil {
		t.Fatal("unignored docs accepted")
	}

	if _, err := EnsureExclude(exclude, "private-docs"); err != nil {
		t.Fatal(err)
	}
	if err := GuardIgnored(ctx, r, "private-docs", exclude); err != nil {
		t.Fatalf("managed exclude rejected: %v", err)
	}

	// A .gitignore rule that also ignores the path must not be accepted as the
	// source: it would require editing a team-tracked file.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/private-docs/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := GuardIgnored(ctx, r, "private-docs", exclude)
	if err == nil {
		t.Fatal("gitignore-sourced ignore accepted")
	}
	if !strings.Contains(err.Error(), ".gitignore") {
		t.Errorf("message should name the source: %q", err.Error())
	}
}

func TestGuardAll(t *testing.T) {
	_, r, _, exclude := guardRepo(t)
	ctx := context.Background()
	if _, err := EnsureExclude(exclude, "private-docs"); err != nil {
		t.Fatal(err)
	}
	if err := GuardAll(ctx, r, "private-docs", exclude); err != nil {
		t.Fatalf("GuardAll rejected a clean repository: %v", err)
	}
}

func TestSnapshotStable(t *testing.T) {
	dir, r, repo, exclude := guardRepo(t)
	ctx := context.Background()

	before, err := TakeSnapshot(ctx, r, repo)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate init's side effects: exclude block, bare store, fetch into it.
	if _, err := EnsureExclude(exclude, "private-docs"); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(dir, ".git", "doc-connector", "store.git")
	store := gitstore.NewStore(r, storeDir)
	if err := store.EnsureBare(ctx, "main"); err != nil {
		t.Fatal(err)
	}

	after, err := TakeSnapshot(ctx, r, repo)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("repository state changed:\n before %+v\n after  %+v", before, after)
	}
}
