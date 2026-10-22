package trees

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/state"
)

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

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func repoWithTree(t *testing.T, pid string) (string, *gitstore.Store, *gitstore.Runner) {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	mustWrite(t, filepath.Join(dir, pid, "a.md"), "a", 0o644)
	mustWrite(t, filepath.Join(dir, pid, "notes", "b.md"), "b", 0o755)
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "seed")
	r := gitstore.New(dir)
	return dir, gitstore.NewStore(r, filepath.Join(dir, ".git")), r
}

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestReadTree(t *testing.T) {
	isolate(t)
	const pid = "abcdefabcdef"
	_, store, _ := repoWithTree(t, pid)
	tree, err := ReadTree(context.Background(), store, "HEAD:"+pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 {
		t.Fatalf("tree = %v", tree)
	}
	if runtime.GOOS != "windows" && tree["notes/b.md"].Mode != 0o100755 {
		t.Errorf("executable mode lost: %o", tree["notes/b.md"].Mode)
	}
	if tree["a.md"].OID == "" {
		t.Error("missing OID")
	}
}

func TestReadTreeRejectsSymlink(t *testing.T) {
	isolate(t)
	const pid = "abcdefabcdef"
	dir, store, _ := repoWithTree(t, pid)
	if err := os.Symlink("a.md", filepath.Join(dir, pid, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "link")
	_, err := ReadTree(context.Background(), store, "HEAD:"+pid)
	if err == nil {
		t.Fatal("symlink accepted")
	}
	if te, ok := err.(*Error); !ok || te.ExitCode() != 3 {
		t.Errorf("error = %v", err)
	}
}

func TestLoadBaseline(t *testing.T) {
	isolate(t)
	const pid = "abcdefabcdef"
	_, store, r := repoWithTree(t, pid)
	tip := strings.TrimSpace(mustRun(t, r, "rev-parse", "HEAD"))
	oid, ok, err := store.TreeOf(context.Background(), tip, pid)
	if err != nil || !ok {
		t.Fatal(err)
	}
	st := state.State{SyncedCommit: tip, BaselineTree: "stale"}
	tree, warnings, err := LoadBaseline(context.Background(), store, st, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || len(warnings) != 1 {
		t.Errorf("tree=%v warnings=%v", tree, warnings)
	}

	empty, warnings, err := LoadBaseline(context.Background(), store, state.State{}, pid)
	if err != nil || len(empty) != 0 || len(warnings) != 0 {
		t.Errorf("empty baseline = %v %v %v", empty, warnings, err)
	}
	_ = oid
}

func mustRun(t *testing.T, r *gitstore.Runner, args ...string) string {
	t.Helper()
	out, err := r.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
