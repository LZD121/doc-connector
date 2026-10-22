package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/state"
)

func TestDiffCommand(t *testing.T) {
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
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(docs, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "one\ntwo\n")
	write("b.md", "gone\n")
	if code, _, errOut := run(t, repo, "push"); code != 0 {
		t.Fatalf("push v1: %s", errOut)
	}
	st, err := state.Load(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	first := st.SyncedCommit

	write("a.md", "one\nTWO\n")
	if err := os.Remove(filepath.Join(docs, "b.md")); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, repo, "push"); code != 0 {
		t.Fatalf("push v2: %s", errOut)
	}
	st, err = state.Load(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	second := st.SyncedCommit

	code, out, errOut := run(t, repo, "diff", first)
	if code != 0 {
		t.Fatalf("diff: code=%d err=%s", code, errOut)
	}
	for _, want := range []string{
		"diff --git a/a.md b/a.md",
		"-two",
		"+TWO",
		"deleted file mode 100644",
		"diff --git a/b.md b/b.md",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("diff output missing %q:\n%s", want, out)
		}
	}

	code, out, errOut = run(t, repo, "diff", first, "a.md")
	if code != 0 {
		t.Fatalf("diff path: code=%d err=%s", code, errOut)
	}
	if strings.Contains(out, "b.md") || !strings.Contains(out, "+TWO") {
		t.Errorf("path filter did not narrow the diff:\n%s", out)
	}

	code, out, _ = run(t, repo, "diff", second)
	if code != 0 || out != "no differences\n" {
		t.Errorf("empty diff: code=%d out=%q", code, out)
	}

	// Positionals may follow flags, and --json emits exactly one document.
	code, out, errOut = run(t, repo, "diff", "--json", first, "a.md")
	if code != 0 {
		t.Fatalf("diff --json: code=%d err=%s", code, errOut)
	}
	doc := decodeOne(t, out)
	if got := doc["command"]; got != "diff" {
		t.Errorf("command = %v", got)
	}
	if got, _ := doc["from"].(string); got != first {
		t.Errorf("from = %q, want %q", got, first)
	}
	if got, _ := doc["to"].(string); got != second {
		t.Errorf("to = %q, want %q", got, second)
	}
	files, _ := doc["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v", doc["files"])
	}
	file, _ := files[0].(map[string]any)
	if file["path"] != "a.md" || file["status"] != "M" {
		t.Errorf("file = %v", file)
	}
	if got := file["insertions"]; got != float64(1) {
		t.Errorf("insertions = %v", got)
	}
	if got := file["deletions"]; got != float64(1) {
		t.Errorf("deletions = %v", got)
	}

	// Full --json includes the deleted file's status too.
	code, out, _ = run(t, repo, "diff", "--json", first)
	if code != 0 {
		t.Fatalf("diff --json all: code=%d", code)
	}
	doc = decodeOne(t, out)
	files, _ = doc["files"].([]any)
	statuses := map[string]string{}
	for _, raw := range files {
		entry, _ := raw.(map[string]any)
		path, _ := entry["path"].(string)
		status, _ := entry["status"].(string)
		statuses[path] = status
	}
	if statuses["a.md"] != "M" || statuses["b.md"] != "D" || len(statuses) != 2 {
		t.Errorf("statuses = %v", statuses)
	}

	// Unknown revisions are usage errors and carry a hint.
	code, out, errOut = run(t, repo, "diff", "no-such-revision")
	if code != CodeUsage {
		t.Errorf("unknown revision: code=%d, want %d", code, CodeUsage)
	}
	if out != "" {
		t.Errorf("unknown revision wrote to stdout: %q", out)
	}
	if !strings.Contains(errOut, "unknown revision") || !strings.Contains(errOut, "hint:") {
		t.Errorf("unknown revision stderr = %q", errOut)
	}

	// Break the remote URL: diff must still work from the local mirror.
	cfg, _, err := config.Load(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Remote = "file://" + filepath.Join(t.TempDir(), "gone.git")
	if err := config.Save(filepath.Join(repo, ".git"), cfg); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = run(t, repo, "diff", first)
	if code != 0 {
		t.Fatalf("offline diff: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "+TWO") || !strings.Contains(out, "deleted file mode") {
		t.Errorf("offline diff lost content:\n%s", out)
	}

	// An edit made after the last push must appear: diff compares the base
	// revision with the working tree, not with the remote-tracking tip. This is
	// the regression the end-to-end smoke test caught.
	write("a.md", "one\nTWO\nthree\n")
	code, out, errOut = run(t, repo, "diff", "--json", second)
	if code != 0 {
		t.Fatalf("diff --json after local edit: code=%d err=%s", code, errOut)
	}
	doc = decodeOne(t, out)
	if got, _ := doc["to"].(string); got != second {
		t.Errorf("to = %q, want %q", got, second)
	}
	files, _ = doc["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("local edit files = %v", doc["files"])
	}
	file, _ = files[0].(map[string]any)
	if file["path"] != "a.md" || file["status"] != "M" {
		t.Errorf("local edit file = %v", file)
	}
	if got := file["insertions"]; got != float64(1) {
		t.Errorf("local edit insertions = %v", got)
	}
	if got := file["deletions"]; got != float64(0) {
		t.Errorf("local edit deletions = %v", got)
	}
	if patch, _ := file["patch"].(string); !strings.Contains(patch, "+three") {
		t.Errorf("local edit patch = %q", patch)
	}
}
