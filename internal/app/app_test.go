package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/state"
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

func setupProject(t *testing.T) (*RepoContext, string) {
	t.Helper()
	isolateGit(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "root")

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	git(t, t.TempDir(), "init", "--bare", "-q", "-b", "main", remoteDir)

	ctx := context.Background()
	r := gitstore.New(repo)
	repoInfo, err := project.Discover(ctx, r, repo)
	if err != nil {
		t.Fatal(err)
	}
	id, err := project.Resolve(ctx, r, repoInfo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Schema:  config.SchemaVersion,
		Remote:  "file://" + remoteDir,
		Branch:  "main",
		DocsDir: "private-docs",
		Machine: "test",
		Project: config.Project{
			ID: id.ID, IDSource: id.Source, RootCommit: id.RootCommit,
			ObjectFormat: id.ObjectFormat, Adoption: "none",
		},
		Limits:    config.DefaultLimits(),
		CreatedAt: state.Now(),
		CreatedBy: "test",
	}
	if err := config.Save(repoInfo.GitDir, cfg); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(repoInfo.GitDir, state.State{ProjectID: id.ID, Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := project.EnsureExclude(project.ExcludePath(repoInfo.CommonDir), cfg.DocsDir); err != nil {
		t.Fatal(err)
	}
	if err := project.GuardAll(ctx, r, cfg.DocsDir, project.ExcludePath(repoInfo.CommonDir)); err != nil {
		t.Fatal(err)
	}

	env := Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rc.Close)
	return rc, repo
}

func TestJournalRecovery(t *testing.T) {
	rc, repo := setupProject(t)
	ctx := context.Background()

	staged := filepath.Join(t.TempDir(), "staged.md")
	if err := os.WriteFile(staged, []byte("recovered"), 0o600); err != nil {
		t.Fatal(err)
	}
	opRoot := filepath.Join(state.TmpDir(rc.Repo.GitDir), "op-test")
	if err := os.MkdirAll(opRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	next := rc.State
	next.SyncedCommit = "abc123"
	next.Branch = "main"
	stateNext := filepath.Join(opRoot, "state.next.json")
	data, _ := json.Marshal(next)
	if err := os.WriteFile(stateNext, data, 0o600); err != nil {
		t.Fatal(err)
	}

	j := &state.Journal{
		Schema:    state.SchemaVersion,
		Op:        "pull",
		ProjectID: rc.Config.Project.ID,
		TmpDir:    opRoot,
		StateNext: stateNext,
		Ops: []state.JournalOp{{
			Action: "write", Path: "notes/recovered.md", Mode: "100644", Staged: staged,
		}},
	}
	if err := state.SaveJournal(rc.Repo.GitDir, j); err != nil {
		t.Fatal(err)
	}

	if err := recoverJournal(ctx, rc); err != nil {
		t.Fatalf("recover: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "private-docs", "notes", "recovered.md"))
	if err != nil || string(data) != "recovered" {
		t.Fatalf("recovered file = %q, %v", data, err)
	}
	if j, _ := state.LoadJournal(rc.Repo.GitDir); j != nil {
		t.Error("journal not removed")
	}
	if _, err := os.Stat(opRoot); !os.IsNotExist(err) {
		t.Error("tmp dir not removed")
	}
	if got, _ := state.Load(rc.Repo.GitDir); got.SyncedCommit != "abc123" {
		t.Errorf("state not advanced: %+v", got)
	}
}

func TestWriteAtomicFailureKeepsTemp(t *testing.T) {
	rc, repo := setupProject(t)
	docs := filepath.Join(repo, "private-docs")
	if err := os.MkdirAll(filepath.Join(docs, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeAtomicFromPath(rc.Root, "x", src, 0o644)
	if err == nil {
		t.Fatal("expected rename failure over a directory")
	}
	matches, _ := filepath.Glob(filepath.Join(docs, ".x.tmp-*"))
	if len(matches) == 0 {
		t.Errorf("temporary file was not preserved: %v", err)
	}
	if info, statErr := os.Stat(filepath.Join(docs, "x")); statErr != nil || !info.IsDir() {
		t.Error("existing directory was damaged")
	}
}

func TestStatusUnreachableRemote(t *testing.T) {
	rc, repo := setupProject(t)
	cfg := *rc.Config
	cfg.Remote = "file://" + filepath.Join(t.TempDir(), "missing.git")
	if err := config.Save(rc.Repo.GitDir, &cfg); err != nil {
		t.Fatal(err)
	}
	rc.Close()

	env := Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}
	res, err := Status(context.Background(), env, false)
	if res == nil {
		t.Fatalf("Status returned no result: %v", err)
	}
	if err == nil {
		t.Fatal("unreachable remote did not produce an error")
	}
	if ae, ok := err.(*Error); !ok || ae.ExitCode() != 4 {
		t.Errorf("error = %v (exit %v)", err, ae)
	}
	if res.RemoteChecked || res.FetchError == nil {
		t.Errorf("remote should be marked unchecked: %+v", res)
	}
}
