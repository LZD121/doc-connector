package app

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LZD121/doc-connector/internal/state"
)

// shortenLockWait keeps lock-contention tests fast; it mirrors the CLI's
// lockWait override in TestInitLockContentionExitsThree.
func shortenLockWait(t *testing.T) {
	t.Helper()
	old := lockWait
	lockWait = 400 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
}

// holdLock takes the project lock as another process would and releases it when
// the test ends.
func holdLock(t *testing.T, gitDir, cmd, machine string) {
	t.Helper()
	lock, err := state.Acquire(context.Background(), gitDir, cmd, machine)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })
}

// requireLockFree proves no command left the project lock held.
func requireLockFree(t *testing.T, gitDir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lock, err := state.Acquire(ctx, gitDir, "test", "test")
	if err != nil {
		t.Fatalf("project lock is still held: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// requireLockedError asserts err is the guard error naming the lock holder.
func requireLockedError(t *testing.T, err error, holder string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a lock error, got nil")
	}
	var ae *Error
	if !errors.As(err, &ae) {
		t.Fatalf("error type = %T (%v), want *app.Error", err, err)
	}
	if ae.ExitCode() != codeGuard {
		t.Errorf("exit code = %d, want %d (%v)", ae.ExitCode(), codeGuard, err)
	}
	if ae.Hint == "" {
		t.Error("lock error has no hint")
	}
	if !strings.Contains(ae.Error(), "holds") || !strings.Contains(ae.Error(), holder) {
		t.Errorf("error does not identify the holder %q: %v", holder, ae)
	}
}

// remoteTip reads refs/heads/main from the private remote without fetching it.
func remoteTip(t *testing.T, url string) string {
	t.Helper()
	out, err := exec.Command("git", "ls-remote", url, "refs/heads/main").Output()
	if err != nil {
		t.Fatalf("ls-remote %s: %v", url, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// seedRemote publishes files under <project_id>/ from a throwaway worktree,
// simulating a second machine.
func seedRemote(t *testing.T, url, projectID string, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	git(t, work, "init", "-q", "-b", "main")
	git(t, work, "remote", "add", "origin", url)
	for rel, content := range files {
		full := filepath.Join(work, projectID, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "seed")
	git(t, work, "push", "-q", "origin", "HEAD:main")
}

// treeListing lists every path below dir, so a stray write or artifact shows up.
func treeListing(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return strings.Join(paths, ",")
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestPushRefusesWhileLocked(t *testing.T) {
	rc, repo := setupProject(t)
	shortenLockWait(t)
	docs := filepath.Join(repo, "private-docs")
	if err := os.WriteFile(filepath.Join(docs, "notes.md"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}

	holdLock(t, rc.Repo.GitDir, "push", "other-machine")

	stateBefore := readFileString(t, state.Path(rc.Repo.GitDir, "state.json"))
	tipBefore := remoteTip(t, rc.Config.Remote)

	env := Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}
	res, err := Push(context.Background(), env, PushOptions{})
	if res != nil {
		t.Errorf("push returned a result while locked: %+v", res)
	}
	requireLockedError(t, err, "other-machine")

	if tipAfter := remoteTip(t, rc.Config.Remote); tipAfter != tipBefore {
		t.Errorf("remote tip moved: %q -> %q", tipBefore, tipAfter)
	}
	if stateAfter := readFileString(t, state.Path(rc.Repo.GitDir, "state.json")); stateAfter != stateBefore {
		t.Errorf("state.json changed while locked:\nbefore %s\nafter  %s", stateBefore, stateAfter)
	}
	if got := treeListing(t, docs); got != "notes.md" {
		t.Errorf("docs directory changed while locked: %q", got)
	}
}

func TestPullRefusesWhileLocked(t *testing.T) {
	rc, repo := setupProject(t)
	shortenLockWait(t)
	seedRemote(t, rc.Config.Remote, rc.Config.Project.ID, map[string]string{"notes.md": "remote"})

	holdLock(t, rc.Repo.GitDir, "pull", "other-machine")

	docs := filepath.Join(repo, "private-docs")
	before := treeListing(t, docs)

	env := Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}
	res, err := Pull(context.Background(), env, PullOptions{})
	if res != nil {
		t.Errorf("pull returned a result while locked: %+v", res)
	}
	requireLockedError(t, err, "other-machine")

	if got := treeListing(t, docs); got != before {
		t.Errorf("docs directory changed while locked: %q -> %q", before, got)
	}
	if j, jerr := state.LoadJournal(rc.Repo.GitDir); jerr != nil {
		t.Fatalf("load journal: %v", jerr)
	} else if j != nil {
		t.Errorf("journal written while locked: %+v", j)
	}
}

func TestProjectLockReleasedOnAllPaths(t *testing.T) {
	rc, repo := setupProject(t)
	shortenLockWait(t)
	if err := os.WriteFile(filepath.Join(repo, "private-docs", "notes.md"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}
	ctx := context.Background()
	// setupProject seeds config and state only; init normally creates the store.
	if err := rc.Store.EnsureBare(ctx, "main"); err != nil {
		t.Fatalf("ensure store: %v", err)
	}

	if _, err := Push(ctx, env, PushOptions{}); err != nil {
		t.Fatalf("push: %v", err)
	}
	requireLockFree(t, rc.Repo.GitDir)

	if _, err := Pull(ctx, env, PullOptions{}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	requireLockFree(t, rc.Repo.GitDir)

	// An early error path: push refuses while a conflict stays unresolved.
	st, err := state.Load(rc.Repo.GitDir)
	if err != nil {
		t.Fatal(err)
	}
	st.Unresolved = append(st.Unresolved, state.Conflict{
		Path: "notes.md",
		Kind: "both-modified",
		At:   state.Now(),
	})
	if err := state.Save(rc.Repo.GitDir, st); err != nil {
		t.Fatal(err)
	}
	_, err = Push(ctx, env, PushOptions{})
	if err == nil {
		t.Fatal("push with an unresolved conflict did not fail")
	}
	var ae *Error
	if !errors.As(err, &ae) || ae.ExitCode() != codeConflict {
		t.Fatalf("error = %v, want exit %d", err, codeConflict)
	}
	requireLockFree(t, rc.Repo.GitDir)
}
