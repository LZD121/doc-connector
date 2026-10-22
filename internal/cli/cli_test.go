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

	for _, name := range []string{"log", "restore"} {
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
	uninitialized := newRepo(t)
	for _, name := range []string{"status", "pull", "push"} {
		code, _, errOut := run(t, uninitialized, name)
		if code != CodeUsage || !strings.Contains(errOut, "not initialized") {
			t.Errorf("%s outside an initialized repo: code=%d stderr=%q", name, code, errOut)
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

// pushRemoteFiles commits files (and removals) on the private remote from a
// throwaway worktree, simulating another machine.
func pushRemoteFiles(t *testing.T, url string, files map[string]string, remove []string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "seed")
	if out, err := exec.Command("git", "clone", "-q", url, work).CombinedOutput(); err != nil {
		if err := os.MkdirAll(work, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, work, "init", "-q", "-b", "main")
		git(t, work, "remote", "add", "origin", url)
		_ = out
	}
	for rel, content := range files {
		full := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range remove {
		if err := os.Remove(filepath.Join(work, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "--allow-empty", "-m", "seed")
	git(t, work, "push", "-q", "origin", "HEAD:main")
	return strings.TrimSpace(git(t, work, "rev-parse", "HEAD"))
}

func projectID(t *testing.T, repo string) string {
	t.Helper()
	root := strings.TrimSpace(git(t, repo, "rev-list", "--max-parents=0", "HEAD"))
	return strings.ToLower(root)[:12]
}

func TestStatusAndPullEndToEnd(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	docs := filepath.Join(repo, "private-docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, repo, "status")
	if code != 0 {
		t.Fatalf("status: %s", errOut)
	}
	if !strings.Contains(out, "status: ahead") || !strings.Contains(out, "a.md") {
		t.Errorf("status output = %q", out)
	}

	code, out, errOut = run(t, repo, "pull")
	if code != 0 || !strings.Contains(out, "pulled: 0 written, 0 deleted") {
		t.Fatalf("pull local-only: code=%d out=%q err=%q", code, out, errOut)
	}

	tip := pushRemoteFiles(t, remote, map[string]string{pid + "/notes/b.md": "remote-b"}, nil)
	// Local a.md is unpushed while the remote gained notes/b.md: both directions
	// changed, so the summary is diverged even though no path conflicts.
	code, out, errOut = run(t, repo, "status")
	if code != 0 || !strings.Contains(out, "status: diverged") || !strings.Contains(out, "notes/b.md") {
		t.Fatalf("status diverged: code=%d out=%q err=%q", code, out, errOut)
	}

	code, out, errOut = run(t, repo, "pull")
	if code != 0 || !strings.Contains(out, "1 written") {
		t.Fatalf("pull write: code=%d out=%q err=%q", code, out, errOut)
	}
	got, err := os.ReadFile(filepath.Join(docs, "notes", "b.md"))
	if err != nil || string(got) != "remote-b" {
		t.Fatalf("pulled content = %q, %v", got, err)
	}
	st, _ := state.Load(filepath.Join(repo, ".git"))
	if st.SyncedCommit != tip {
		t.Errorf("baseline = %s, want %s", st.SyncedCommit, tip)
	}

	// A local-only file must survive a remote deletion.
	if err := os.WriteFile(filepath.Join(docs, "local.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	pushRemoteFiles(t, remote, nil, []string{pid + "/notes/b.md"})
	code, out, errOut = run(t, repo, "pull")
	if code != 0 || !strings.Contains(out, "1 deleted") {
		t.Fatalf("pull delete: code=%d out=%q err=%q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(docs, "notes", "b.md")); !os.IsNotExist(err) {
		t.Error("remote-deleted file still present")
	}
	if data, err := os.ReadFile(filepath.Join(docs, "local.md")); err != nil || string(data) != "mine" {
		t.Errorf("local-only file was touched: %q %v", data, err)
	}

	code, out, errOut = run(t, repo, "status")
	if code != 0 || !strings.Contains(out, "status: ahead") || !strings.Contains(out, "local.md") {
		t.Errorf("final status: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestPullConflictRefuses(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	pushRemoteFiles(t, remote, map[string]string{pid + "/a.md": "remote-1"}, nil)
	if code, _, errOut := run(t, repo, "pull"); code != 0 {
		t.Fatalf("initial pull: %s", errOut)
	}
	docs := filepath.Join(repo, "private-docs", "a.md")
	if err := os.WriteFile(docs, []byte("local-change"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := state.Load(filepath.Join(repo, ".git"))
	pushRemoteFiles(t, remote, map[string]string{pid + "/a.md": "remote-2"}, nil)
	code, out, _ := run(t, repo, "status")
	if code != 0 || !strings.Contains(out, "status: diverged") || !strings.Contains(out, "C a.md") {
		t.Errorf("status did not name the conflict: code=%d out=%q", code, out)
	}

	code, _, errOut := run(t, repo, "pull")
	if code != CodeConflict {
		t.Fatalf("conflict pull code = %d, want 1; stderr=%q", code, errOut)
	}
	data, _ := os.ReadFile(docs)
	if string(data) != "local-change" {
		t.Errorf("local file overwritten: %q", data)
	}
	after, _ := state.Load(filepath.Join(repo, ".git"))
	if after.SyncedCommit != before.SyncedCommit {
		t.Errorf("baseline advanced on conflict")
	}
}

func TestPullDryRun(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	pushRemoteFiles(t, remote, map[string]string{pid + "/c.md": "c"}, nil)
	before, _ := state.Load(filepath.Join(repo, ".git"))

	code, out, errOut := run(t, repo, "pull", "--dry-run")
	if code != 0 || !strings.Contains(out, "dry-run: would write 1") {
		t.Fatalf("dry-run: code=%d out=%q err=%q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(repo, "private-docs", "c.md")); !os.IsNotExist(err) {
		t.Error("dry-run wrote a file")
	}
	after, _ := state.Load(filepath.Join(repo, ".git"))
	if after.SyncedCommit != before.SyncedCommit {
		t.Error("dry-run advanced the baseline")
	}
}

func TestPullFailureKeepsBaseline(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	// A regular file blocks the directory the remote wants to create.
	if err := os.MkdirAll(filepath.Join(repo, "private-docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "private-docs", "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := state.Load(filepath.Join(repo, ".git"))
	pushRemoteFiles(t, remote, map[string]string{pid + "/blocker/f.md": "nested"}, nil)

	code, _, errOut := run(t, repo, "pull")
	if code != CodeConflict {
		t.Fatalf("code = %d, want 1; stderr=%q", code, errOut)
	}
	after, _ := state.Load(filepath.Join(repo, ".git"))
	if after.SyncedCommit != before.SyncedCommit {
		t.Error("baseline advanced after a failed apply")
	}
	if j, _ := state.LoadJournal(filepath.Join(repo, ".git")); j == nil {
		t.Error("journal was removed after a failed apply")
	}
}

func TestPullRejectsUnsafeRemotePaths(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	pushRemoteFiles(t, remote, map[string]string{pid + "/NUL.txt": "reserved"}, nil)

	code, _, errOut := run(t, repo, "pull")
	if code != CodeGuard {
		t.Fatalf("code = %d, want 3; stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(repo, "private-docs", "NUL.txt")); !os.IsNotExist(err) {
		t.Error("unsafe path was written")
	}
}

func remoteDir(url string) string { return strings.TrimPrefix(url, "file://") }

func TestPushEndToEnd(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	docs := filepath.Join(repo, "private-docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, repo, "push")
	if code != 0 || !strings.Contains(out, "pushed:") {
		t.Fatalf("push: code=%d out=%q err=%q", code, out, errOut)
	}
	rp := remoteDir(remote)
	if got := git(t, rp, "show", "main:"+pid+"/a.md"); strings.TrimSpace(got) != "one" {
		t.Errorf("remote content = %q", got)
	}
	st, _ := state.Load(filepath.Join(repo, ".git"))
	if st.SyncedCommit == "" {
		t.Fatal("baseline not advanced after push")
	}
	msg := git(t, rp, "log", "-1", "--format=%B", "main")
	for _, trailer := range []string{"Doc-Connector-Project: " + pid, "Doc-Connector-Machine: ", "Doc-Connector-Files: "} {
		if !strings.Contains(msg, trailer) {
			t.Errorf("commit message missing %q:\n%s", trailer, msg)
		}
	}

	// Another project's root entry must survive.
	pushRemoteFiles(t, remote, map[string]string{"other-project/r.md": "keep"}, nil)
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, repo, "push"); code != 0 {
		t.Fatalf("second push: %s", errOut)
	}
	if got := git(t, rp, "show", "main:other-project/r.md"); strings.TrimSpace(got) != "keep" {
		t.Errorf("other project entry lost: %q", got)
	}

	// No local change: no-op, byte-identical state.
	before, _ := state.Load(filepath.Join(repo, ".git"))
	code, out, errOut = run(t, repo, "push")
	if code != 0 || !strings.Contains(out, "no changes") {
		t.Fatalf("no-op push: code=%d out=%q err=%q", code, out, errOut)
	}
	after, _ := state.Load(filepath.Join(repo, ".git"))
	if after.SyncedCommit != before.SyncedCommit {
		t.Error("no-op push moved the baseline")
	}
}

func TestPushClobberAfterOtherMachine(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init A: %s", errOut)
	}
	docs := filepath.Join(repo, "private-docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("A1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, repo, "push"); code != 0 {
		t.Fatalf("push A: %s", errOut)
	}

	clone := filepath.Join(t.TempDir(), "machine-b")
	git(t, t.TempDir(), "clone", "-q", "file://"+repo, clone)
	if code, _, errOut := run(t, clone, "init", "--remote", remote); code != 0 {
		t.Fatalf("init B: %s", errOut)
	}
	if code, _, errOut := run(t, clone, "pull"); code != 0 {
		t.Fatalf("pull B: %s", errOut)
	}
	if err := os.WriteFile(filepath.Join(clone, "private-docs", "a.md"), []byte("B1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, clone, "push"); code != 0 {
		t.Fatalf("push B: %s", errOut)
	}

	// A has not seen B's version; pushing its own change must refuse.
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("A2"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := state.Load(filepath.Join(repo, ".git"))
	code, _, errOut := run(t, repo, "push")
	if code != CodeConflict {
		t.Fatalf("clobber push code = %d, want 1; stderr=%q", code, errOut)
	}
	if !strings.Contains(errOut, "pull") {
		t.Errorf("stderr = %q", errOut)
	}
	after, _ := state.Load(filepath.Join(repo, ".git"))
	if after.SyncedCommit != before.SyncedCommit {
		t.Error("baseline advanced after a refused push")
	}
}

func TestPushResolve(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	docs := filepath.Join(repo, "private-docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, repo, "push"); code != 0 {
		t.Fatalf("push: %s", errOut)
	}
	// Local edit plus a remote edit of the same path is the conflict case.
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("A-local"), 0o644); err != nil {
		t.Fatal(err)
	}
	pushRemoteFiles(t, remote, map[string]string{pid + "/a.md": "B"}, nil)

	code, _, errOut := run(t, repo, "pull")
	if code != CodeConflict || !strings.Contains(errOut, "conflicting") {
		t.Fatalf("conflict pull: code=%d stderr=%q", code, errOut)
	}
	if data, _ := os.ReadFile(filepath.Join(docs, "a.md")); string(data) != "A-local" {
		t.Errorf("local content changed: %q", data)
	}
	artifacts, _ := filepath.Glob(filepath.Join(docs, "a.md.theirs.*"))
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %v", artifacts)
	}
	if data, _ := os.ReadFile(artifacts[0]); string(data) != "B" {
		t.Errorf("artifact content = %q", data)
	}
	st, _ := state.Load(filepath.Join(repo, ".git"))
	if len(st.Unresolved) != 1 || st.Unresolved[0].Path != "a.md" {
		t.Fatalf("unresolved = %+v", st.Unresolved)
	}

	code, out, _ := run(t, repo, "status")
	if strings.Contains(out, ".theirs.") {
		t.Errorf("status treated an artifact as a document: %q", out)
	}

	if code, _, errOut := run(t, repo, "push", "--resolve", "a.md"); code != 0 {
		t.Fatalf("resolve push: %s", errOut)
	}
	if got := git(t, remoteDir(remote), "show", "main:"+pid+"/a.md"); strings.TrimSpace(got) != "A-local" {
		t.Errorf("remote content after resolve = %q", got)
	}
	if remaining, _ := filepath.Glob(filepath.Join(docs, "a.md.theirs.*")); len(remaining) != 0 {
		t.Errorf("artifact not removed: %v", remaining)
	}
	st, _ = state.Load(filepath.Join(repo, ".git"))
	if len(st.Unresolved) != 0 {
		t.Errorf("unresolved not cleared: %+v", st.Unresolved)
	}
}

func TestPushDryRun(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	docs := filepath.Join(repo, "private-docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := state.Load(filepath.Join(repo, ".git"))

	code, out, errOut := run(t, repo, "push", "--dry-run")
	if code != 0 || !strings.Contains(out, "dry-run: would commit") {
		t.Fatalf("dry-run: code=%d out=%q err=%q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "doc-connector", "store.git", "refs", "heads", "main")); !os.IsNotExist(err) {
		t.Error("dry-run created a remote branch ref")
	}
	after, _ := state.Load(filepath.Join(repo, ".git"))
	if after.SyncedCommit != before.SyncedCommit {
		t.Error("dry-run advanced the baseline")
	}
	if data, _ := os.ReadFile(filepath.Join(docs, "a.md")); string(data) != "one" {
		t.Error("dry-run changed docs")
	}
}

func TestPullConflictShapes(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	docs := filepath.Join(repo, "private-docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep.md", "gone.md", "localdel.md"} {
		if err := os.WriteFile(filepath.Join(docs, name), []byte("base"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, errOut := run(t, repo, "push"); code != 0 {
		t.Fatalf("push: %s", errOut)
	}

	// local modified + remote deleted -> no artifact, unresolved only
	if err := os.WriteFile(filepath.Join(docs, "keep.md"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	// local add + remote add of the same path -> add/add artifact
	if err := os.WriteFile(filepath.Join(docs, "new.md"), []byte("local-new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// local deleted + remote modified -> artifact in original parent
	if err := os.Remove(filepath.Join(docs, "localdel.md")); err != nil {
		t.Fatal(err)
	}
	pushRemoteFiles(t, remote, map[string]string{
		pid + "/gone.md":     "remote-new",
		pid + "/localdel.md": "remote-mod",
		pid + "/new.md":      "remote-new-file",
	}, []string{pid + "/keep.md"})

	code, _, errOut := run(t, repo, "pull")
	if code != CodeConflict {
		t.Fatalf("code = %d, want 1; stderr=%q", code, errOut)
	}
	if data, _ := os.ReadFile(filepath.Join(docs, "keep.md")); string(data) != "local" {
		t.Errorf("locally modified file changed: %q", data)
	}
	if artifacts, _ := filepath.Glob(filepath.Join(docs, "keep.md.theirs.*")); len(artifacts) != 0 {
		t.Errorf("modify/delete wrote an artifact: %v", artifacts)
	}
	if _, err := os.Stat(filepath.Join(docs, "localdel.md")); !os.IsNotExist(err) {
		t.Error("locally deleted file was resurrected")
	}
	artifacts, _ := filepath.Glob(filepath.Join(docs, "localdel.md.theirs.*"))
	if len(artifacts) != 1 {
		t.Fatalf("delete/modify artifact = %v", artifacts)
	}
	if data, _ := os.ReadFile(artifacts[0]); string(data) != "remote-mod" {
		t.Errorf("artifact content = %q", data)
	}
	addAdd, _ := filepath.Glob(filepath.Join(docs, "new.md.theirs.*"))
	if len(addAdd) != 1 {
		t.Fatalf("add/add artifact = %v", addAdd)
	}
	if data, _ := os.ReadFile(addAdd[0]); string(data) != "remote-new-file" {
		t.Errorf("add/add artifact content = %q", data)
	}

	// A second pull must not stack another artifact on the same path.
	code, _, _ = run(t, repo, "pull")
	if code != CodeConflict {
		t.Fatalf("second pull code = %d, want 1", code)
	}
	if again, _ := filepath.Glob(filepath.Join(docs, "localdel.md.theirs.*")); len(again) != 1 {
		t.Errorf("duplicate artifacts after second pull: %v", again)
	}
}

func TestTwoProjectsSameRemote(t *testing.T) {
	isolateGit(t)
	remote := emptyBareRemote(t)

	repoA := newRepo(t)
	if code, _, errOut := run(t, repoA, "init", "--remote", remote); code != 0 {
		t.Fatalf("init A: %s", errOut)
	}
	pidA := projectID(t, repoA)
	if err := os.MkdirAll(filepath.Join(repoA, "private-docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoA, "private-docs", "a.md"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, repoA, "push"); code != 0 {
		t.Fatalf("push A: %s", errOut)
	}

	repoB := newRepo(t)
	git(t, repoB, "commit", "--amend", "-q", "-m", "unique root for project B")
	if code, _, errOut := run(t, repoB, "init", "--remote", remote); code != 0 {
		t.Fatalf("init B: %s", errOut)
	}
	pidB := projectID(t, repoB)
	if pidB == pidA {
		t.Fatal("two different repositories derived the same id")
	}
	if err := os.MkdirAll(filepath.Join(repoB, "private-docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoB, "private-docs", "b.md"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, repoB, "push"); code != 0 {
		t.Fatalf("push B: %s", errOut)
	}

	rp := remoteDir(remote)
	if got := git(t, rp, "show", "main:"+pidA+"/a.md"); strings.TrimSpace(got) != "A" {
		t.Errorf("A prefix lost: %q", got)
	}
	if got := git(t, rp, "show", "main:"+pidB+"/b.md"); strings.TrimSpace(got) != "B" {
		t.Errorf("B prefix missing: %q", got)
	}
}
