package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/state"
)

func run(t *testing.T, cwd string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = Run(context.Background(), args, Env{
		Stdout:  &out,
		Stderr:  &errBuf,
		Stdin:   strings.NewReader(""),
		Version: "1.2.3",
		Commit:  "abc1234",
		Cwd:     cwd,
	})
	return code, out.String(), errBuf.String()
}

func TestRootSurface(t *testing.T) {
	dir := t.TempDir()

	code, out, errOut := run(t, dir, "--version")
	if code != 0 || !strings.Contains(out, "doc-connector 1.2.3") || errOut != "" {
		t.Errorf("--version: code=%d out=%q err=%q", code, out, errOut)
	}

	code, out, errOut = run(t, dir, "--help")
	if code != 0 || !strings.Contains(out, "Usage:") || errOut != "" {
		t.Errorf("--help: code=%d out=%q err=%q", code, out, errOut)
	}

	code, out, errOut = run(t, dir)
	if code != CodeUsage || out != "" || !strings.Contains(errOut, "Usage:") {
		t.Errorf("no args: code=%d out=%q err=%q", code, out, errOut)
	}

	code, out, errOut = run(t, dir, "bogus")
	if code != CodeUsage || out != "" || !strings.Contains(errOut, `unknown command "bogus"`) {
		t.Errorf("unknown command: code=%d out=%q err=%q", code, out, errOut)
	}

	code, out, errOut = run(t, dir, "--nope")
	if code != CodeUsage || !strings.Contains(errOut, "unknown flag") {
		t.Errorf("unknown flag: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestCommandHelpAndStubs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"init", "status", "push", "pull", "log", "restore"} {
		code, out, errOut := run(t, dir, name, "--help")
		if code != 0 {
			t.Errorf("%s --help: code = %d, stderr = %q", name, code, errOut)
		}
		if !strings.Contains(out, "Usage:") || !strings.Contains(out, name) {
			t.Errorf("%s --help output missing usage: %q", name, out)
		}
		if errOut != "" {
			t.Errorf("%s --help wrote diagnostics: %q", name, errOut)
		}
	}

	for _, name := range []string{"status", "push", "pull", "log", "restore"} {
		code, out, errOut := run(t, dir, name)
		if code != CodeUsage {
			t.Errorf("%s: code = %d, want 2", name, code)
		}
		if out != "" {
			t.Errorf("%s wrote to stdout: %q", name, out)
		}
		if !strings.Contains(errOut, "not implemented yet") {
			t.Errorf("%s stderr = %q, want not-implemented", name, errOut)
		}
	}
}

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

func emptyBareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	git(t, t.TempDir(), "init", "--bare", "-q", "-b", "main", dir)
	return "file://" + dir
}

func TestInitEndToEnd(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)

	runner := gitstore.New(repo)
	repoInfo, err := project.Discover(context.Background(), runner, repo)
	if err != nil {
		t.Fatal(err)
	}
	before, err := project.TakeSnapshot(context.Background(), runner, repoInfo)
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, repo, "init", "--remote", remote)
	if code != 0 {
		t.Fatalf("init failed: code=%d out=%q err=%q", code, out, errOut)
	}
	for _, want := range []string{"project: ", "remote: ", "docs dir: private-docs", "base: none", "next: doc-connector push"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}

	cfg, _, err := config.Load(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Remote != remote || cfg.Branch != "main" || cfg.DocsDir != "private-docs" {
		t.Errorf("config = %+v", cfg)
	}
	if cfg.Project.IDSource != "derived" || len(cfg.Project.ID) != 12 {
		t.Errorf("project = %+v", cfg.Project)
	}

	st, err := state.Load(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if st.ProjectID != cfg.Project.ID || st.SyncedCommit != "" {
		t.Errorf("state = %+v", st)
	}

	// The team repository must look untouched and the docs must stay hidden.
	status := git(t, repo, "status", "--porcelain")
	if strings.Contains(status, "private-docs") || strings.Contains(status, "config.yaml") {
		t.Errorf("git status shows hidden paths:\n%s", status)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Error("init created .gitignore")
	}

	after, err := project.TakeSnapshot(context.Background(), runner, repoInfo)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("init changed the team repository:\n before %+v\n after  %+v", before, after)
	}

	// Second run: byte-identical config/state and identical output.
	cfgPath := filepath.Join(repo, ".git", "doc-connector", "config.yaml")
	stPath := filepath.Join(repo, ".git", "doc-connector", "state.json")
	cfgBefore, _ := os.ReadFile(cfgPath)
	stBefore, _ := os.ReadFile(stPath)

	code, out2, errOut2 := run(t, repo, "init", "--remote", remote)
	if code != 0 {
		t.Fatalf("second init failed: %q", errOut2)
	}
	if out2 != out {
		t.Errorf("second init output differs:\n%s\n---\n%s", out, out2)
	}
	cfgAfter, _ := os.ReadFile(cfgPath)
	stAfter, _ := os.ReadFile(stPath)
	if string(cfgBefore) != string(cfgAfter) {
		t.Errorf("config changed on re-run:\n%s\n---\n%s", cfgBefore, cfgAfter)
	}
	if string(stBefore) != string(stAfter) {
		t.Errorf("state changed on re-run:\n%s\n---\n%s", stBefore, stAfter)
	}
}

func TestInitAdoptsExistingPrefix(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	rootHex := strings.TrimSpace(git(t, repo, "rev-list", "--max-parents=0", "HEAD"))
	pid := strings.ToLower(rootHex)[:12]

	remote := emptyBareRemote(t)
	seed := t.TempDir()
	git(t, seed, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(seed, pid, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, pid, "notes", "a.md"), []byte("remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-q", "-m", "seed")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "-q", "origin", "main")
	tip := strings.TrimSpace(git(t, seed, "rev-parse", "HEAD"))

	code, out, errOut := run(t, repo, "init", "--remote", remote)
	if code != 0 {
		t.Fatalf("init failed: %s", errOut)
	}
	if !strings.Contains(out, "pull") || !strings.Contains(out, "not modified") {
		t.Errorf("stdout = %q", out)
	}
	st, err := state.Load(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if st.SyncedCommit != tip || st.BaselineTree == "" {
		t.Errorf("state did not adopt the prefix: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(repo, "private-docs")); !os.IsNotExist(err) {
		t.Error("init modified local docs")
	}
}

func TestInitSecondMachine(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	code, outA, errOut := run(t, repo, "init", "--remote", remote)
	if code != 0 {
		t.Fatalf("machine A init: %s", errOut)
	}
	idA := firstProjectID(t, outA)

	clone := filepath.Join(t.TempDir(), "clone")
	git(t, t.TempDir(), "clone", "-q", "file://"+repo, clone)

	code, outB, errOut := run(t, clone, "init", "--remote", remote)
	if code != 0 {
		t.Fatalf("machine B init: %s", errOut)
	}
	idB := firstProjectID(t, outB)
	if idA != idB {
		t.Errorf("project ids differ: A=%s B=%s", idA, idB)
	}
}

func firstProjectID(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "project: ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	t.Fatalf("no project line in %q", out)
	return ""
}

func TestInitRefusesTrackedDocs(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "private-docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "private-docs", "secret.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-f", "private-docs/secret.md")
	git(t, repo, "commit", "-q", "-m", "oops")

	remote := emptyBareRemote(t)
	code, _, errOut := run(t, repo, "init", "--remote", remote)
	if code != CodeGuard {
		t.Fatalf("code = %d, want 3; stderr=%q", code, errOut)
	}
	if !strings.Contains(errOut, "tracked") {
		t.Errorf("stderr = %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "doc-connector", "config.yaml")); !os.IsNotExist(err) {
		t.Error("config was written despite guard failure")
	}
}

func TestInitRefusesForeignIgnoreSource(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("/private-docs/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := emptyBareRemote(t)
	code, _, errOut := run(t, repo, "init", "--remote", remote)
	if code != CodeGuard {
		t.Fatalf("code = %d, want 3; stderr=%q", code, errOut)
	}
	if !strings.Contains(errOut, ".gitignore") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestInitUnreachableRemoteExitsFour(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	code, _, errOut := run(t, repo, "init", "--remote", "file://"+filepath.Join(t.TempDir(), "missing.git"))
	if code != CodeRemote {
		t.Fatalf("code = %d, want 4; stderr=%q", code, errOut)
	}
}

func TestInitRequiresRemote(t *testing.T) {
	code, _, errOut := run(t, t.TempDir(), "init")
	if code != CodeUsage || !strings.Contains(errOut, "--remote is required") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestInitOutsideRepository(t *testing.T) {
	isolateGit(t)
	code, _, errOut := run(t, t.TempDir(), "init", "--remote", "file:///tmp/r.git")
	if code != CodeUsage || !strings.Contains(errOut, "not inside a git repository") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestInitLockContentionExitsThree(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)

	old := lockWait
	lockWait = 400 * time.Millisecond
	defer func() { lockWait = old }()

	lock, err := state.Acquire(context.Background(), filepath.Join(repo, ".git"), "init", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	code, _, errOut := run(t, repo, "init", "--remote", "file:///tmp/r.git")
	if code != CodeGuard {
		t.Fatalf("code = %d, want 3; stderr=%q", code, errOut)
	}
	if !strings.Contains(errOut, "holds") {
		t.Errorf("stderr = %q", errOut)
	}
}
