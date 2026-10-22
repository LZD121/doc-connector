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

func TestInitResolvesIdentityAndRefusesBootstrap(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)

	code, out, errOut := run(t, repo, "init", "--remote", "file:///tmp/remote.git")
	if code != CodeUsage {
		t.Fatalf("code = %d, want 2; stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "project: ") || !strings.Contains(out, "derived") {
		t.Errorf("stdout = %q, want resolved identity", out)
	}
	if !strings.Contains(errOut, "not implemented yet") {
		t.Errorf("stderr = %q", errOut)
	}
	for _, name := range []string{"config.yaml", "state.json"} {
		if _, err := os.Stat(filepath.Join(repo, ".git", "doc-connector", name)); !os.IsNotExist(err) {
			t.Errorf("init wrote %s before bootstrap: %v", name, err)
		}
	}
}

func TestInitRejectsMismatchedExplicitID(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	derived := git(t, repo, "rev-list", "--max-parents=0", "HEAD")
	derived = strings.ToLower(strings.TrimSpace(derived))[:12]

	mismatch := "0123456789ab"
	if mismatch == derived {
		mismatch = "abcdefabcdef"
	}

	code, _, errOut := run(t, repo, "init", "--remote", "file:///tmp/r.git", "--project-id", mismatch)
	if code != CodeUsage {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(errOut, derived) || !strings.Contains(errOut, mismatch) {
		t.Errorf("stderr must name both ids: %q", errOut)
	}

	code, out, errOut := run(t, repo, "init", "--remote", "file:///tmp/r.git", "--project-id", mismatch, "--force")
	if code != CodeUsage || !strings.Contains(errOut, "not implemented yet") {
		t.Fatalf("forced mismatch: code=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "explicit") {
		t.Errorf("stdout = %q, want id_source explicit", out)
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
