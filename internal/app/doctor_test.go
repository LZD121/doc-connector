package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/state"
)

func doctorEnv(repo string) Env {
	return Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}
}

func checkIDs(res *DoctorResult) []string {
	ids := make([]string, 0, len(res.Checks))
	for _, c := range res.Checks {
		ids = append(ids, c.ID)
	}
	return ids
}

func findCheck(t *testing.T, res *DoctorResult, id string) Check {
	t.Helper()
	for _, c := range res.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q missing; got %v", id, checkIDs(res))
	return Check{}
}

// TestDoctorClean requires a fetched mirror: doctor itself never fetches, so the
// remote check would fail on a project that has never reached its remote.
func TestDoctorClean(t *testing.T) {
	rc, repo := setupProject(t)
	ctx := context.Background()
	git(t, repo, "push", "-q", rc.Config.Remote, "main")
	if err := rc.Store.EnsureBare(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Fetch(ctx, "test"); err != nil {
		t.Fatal(err)
	}

	res, err := Doctor(ctx, doctorEnv(repo))
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if res.Summary.Fail != 0 {
		t.Fatalf("summary = %+v; checks = %+v", res.Summary, res.Checks)
	}
	if res.Summary.OK+res.Summary.Warn+res.Summary.Fail != len(res.Checks) {
		t.Errorf("summary = %+v over %d checks", res.Summary, len(res.Checks))
	}
	want := []string{"git-version", "repository", "exclude", "untracked", "ignored", "config", "lock", "remote", "artifacts"}
	got := strings.Join(checkIDs(res), ",")
	if got != strings.Join(want, ",") {
		t.Errorf("check order = %s, want %s", got, strings.Join(want, ","))
	}
	if c := findCheck(t, res, "remote"); c.Status != CheckOK {
		t.Errorf("remote = %+v", c)
	}
	if res.RemoteUnreachable {
		t.Error("RemoteUnreachable set on a fetched project")
	}
}

func TestDoctorTamperedExclude(t *testing.T) {
	rc, repo := setupProject(t)
	excludePath := project.ExcludePath(rc.Repo.CommonDir)
	tampered := project.ExcludeBegin + "\n/wrong-docs/\n" + project.ExcludeEnd + "\n"
	if err := os.WriteFile(excludePath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Doctor(context.Background(), doctorEnv(repo))
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	c := findCheck(t, res, "exclude")
	if c.Status != CheckFail {
		t.Fatalf("exclude = %+v, want fail", c)
	}
	if !strings.Contains(c.Detail, excludePath) {
		t.Errorf("detail %q does not name %s", c.Detail, excludePath)
	}
	if !strings.Contains(c.Detail, "wrong-docs") {
		t.Errorf("detail %q does not show the stray pattern", c.Detail)
	}
	if c.Hint == "" {
		t.Error("exclude failure has no hint")
	}
	if res.Summary.Fail == 0 {
		t.Errorf("summary = %+v", res.Summary)
	}
}

func TestDoctorConfigMismatch(t *testing.T) {
	rc, repo := setupProject(t)
	st, err := state.Load(rc.Repo.GitDir)
	if err != nil {
		t.Fatal(err)
	}
	st.ProjectID = "000000000000"
	if err := state.Save(rc.Repo.GitDir, st); err != nil {
		t.Fatal(err)
	}

	res, err := Doctor(context.Background(), doctorEnv(repo))
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	c := findCheck(t, res, "config")
	if c.Status != CheckFail {
		t.Fatalf("config = %+v, want fail", c)
	}
	if !strings.Contains(c.Detail, rc.Config.Project.ID) || !strings.Contains(c.Detail, "000000000000") {
		t.Errorf("detail %q does not name both ids", c.Detail)
	}
}

func TestDoctorLiveLock(t *testing.T) {
	rc, repo := setupProject(t)
	held, err := state.Acquire(context.Background(), rc.Repo.GitDir, "push", "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer held.Release()

	res, err := Doctor(context.Background(), doctorEnv(repo))
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	c := findCheck(t, res, "lock")
	if c.Status != CheckWarn {
		t.Fatalf("lock = %+v, want warn", c)
	}
	if !strings.Contains(c.Detail, strconv.Itoa(os.Getpid())) {
		t.Errorf("detail %q does not name pid %d", c.Detail, os.Getpid())
	}
	if !strings.Contains(c.Detail, "push") {
		t.Errorf("detail %q does not name the command", c.Detail)
	}
}

func TestDoctorArtifacts(t *testing.T) {
	rc, repo := setupProject(t)
	tmp := state.TmpDir(rc.Repo.GitDir)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(tmp, "op-crashed")
	if err := os.WriteFile(leftover, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Doctor(context.Background(), doctorEnv(repo))
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	c := findCheck(t, res, "artifacts")
	if c.Status != CheckWarn {
		t.Fatalf("artifacts = %+v, want warn", c)
	}
	if !strings.Contains(c.Detail, leftover) {
		t.Errorf("detail %q does not name %s", c.Detail, leftover)
	}
}

func TestDoctorReadOnly(t *testing.T) {
	isolateGit(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "root")

	res, err := Doctor(context.Background(), doctorEnv(repo))
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if c := findCheck(t, res, "config"); c.Status != CheckFail {
		t.Errorf("config = %+v, want fail for an uninitialized repository", c)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "doc-connector")); !os.IsNotExist(err) {
		t.Errorf("doctor created .git/doc-connector (stat err = %v)", err)
	}
	if status := strings.TrimSpace(git(t, repo, "status", "--porcelain")); status != "" {
		t.Errorf("doctor dirtied the work tree: %q", status)
	}
}

func TestDoctorNotRepository(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()

	res, err := Doctor(context.Background(), doctorEnv(dir))
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	ids := checkIDs(res)
	if len(ids) != 2 || ids[0] != "git-version" || ids[1] != "repository" {
		t.Fatalf("checks = %v, want git-version then repository", ids)
	}
	c := findCheck(t, res, "repository")
	if c.Status != CheckFail {
		t.Fatalf("repository = %+v, want fail", c)
	}
	if !strings.Contains(c.Detail, "skipped") {
		t.Errorf("detail %q does not say the scoped checks were skipped", c.Detail)
	}
	if res.Summary.Fail == 0 {
		t.Errorf("summary = %+v", res.Summary)
	}
}

func TestDoctorCanceled(t *testing.T) {
	_, repo := setupProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Doctor(ctx, doctorEnv(repo))
	if err == nil {
		t.Fatalf("canceled doctor returned %+v", res)
	}
}
