package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRepoAt initializes a git repository at dir. It exists next to newRepo,
// which always allocates its own temporary directory, because workspace tests
// must control the parent layout.
func newRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

// initRepoAt creates a repository and binds it to a fresh empty remote.
func initRepoAt(t *testing.T, dir string) string {
	t.Helper()
	newRepoAt(t, dir)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, dir, "init", "--remote", remote); code != 0 {
		t.Fatalf("init %s: code=%d err=%s", dir, code, errOut)
	}
	return remote
}

// threeRepoRoot builds clean, ahead and broken repositories under one root:
// clean is in sync, ahead has an unpublished file, broken has a corrupt config.
func threeRepoRoot(t *testing.T) (root string, ids map[string]string) {
	t.Helper()
	root = t.TempDir()
	ids = map[string]string{}
	for _, name := range []string{"clean", "ahead", "broken"} {
		dir := filepath.Join(root, name)
		initRepoAt(t, dir)
		ids[name] = projectID(t, dir)
	}
	// init does not create the docs directory; the ahead repository needs one.
	if err := os.MkdirAll(filepath.Join(root, "ahead", "private-docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ahead", "private-docs", "a.md"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken", ".git", "doc-connector", "config.yaml"),
		[]byte("remote: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, ids
}

func TestStatusRootAggregates(t *testing.T) {
	isolateGit(t)
	root, ids := threeRepoRoot(t)
	broken := filepath.Join(root, "broken")

	code, out, errOut := run(t, root, "status", "--root", root)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (the broken repository); out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "workspace: "+root+" (3 repos, 0 skipped)") {
		t.Errorf("header missing: %q", out)
	}
	for _, want := range []string{
		fmt.Sprintf("clean  %s  clean  a0/b0  c0  current", ids["clean"]),
		fmt.Sprintf("ahead  %s  ahead  a1/b0  c0  ahead", ids["ahead"]),
		"broken  -  -  a0/b0  c0  failed",
		"summary: current=1 ahead=1 behind=0 blocked=0 failed=1 locked=0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
	if !strings.Contains(out, "  parse "+filepath.Join(broken, ".git", "doc-connector")) {
		t.Errorf("failing repository did not print its error text: %q", out)
	}
	if !strings.Contains(errOut, "1 of 3 repositories need attention") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestStatusRootJSONDocument(t *testing.T) {
	isolateGit(t)
	root, ids := threeRepoRoot(t)

	code, out, errOut := run(t, root, "status", "--root", root, "--json")
	if code != 2 {
		t.Fatalf("code = %d, want 2; err=%q", code, errOut)
	}
	doc := decodeOne(t, out) // fails on a second document
	if doc["command"] != "status" || doc["root"] != root {
		t.Errorf("document head = %v", doc)
	}
	if doc["ok"] != false {
		t.Errorf("ok = %v, want false", doc["ok"])
	}
	if e, ok := doc["error"].(map[string]any); !ok || e["code"] != "usage" {
		t.Errorf("error = %v", doc["error"])
	}

	repos, ok := doc["repos"].([]any)
	if !ok || len(repos) != 3 {
		t.Fatalf("repos = %v", doc["repos"])
	}
	byName := map[string]map[string]any{}
	for _, item := range repos {
		repo, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("repo entry = %v", item)
		}
		byName[repo["name"].(string)] = repo
	}

	clean := byName["clean"]
	if clean["project_id"] != ids["clean"] || clean["docs_dir"] != "private-docs" ||
		clean["state"] != "clean" || clean["verdict"] != "current" || clean["error_code"] != float64(0) {
		t.Errorf("clean entry = %v", clean)
	}
	if _, present := clean["error"]; present {
		t.Errorf("clean entry carries an error: %v", clean["error"])
	}
	if _, present := clean["path"]; !present {
		t.Errorf("clean entry has no path: %v", clean)
	}
	ahead := byName["ahead"]
	if ahead["verdict"] != "ahead" || ahead["ahead"] != float64(1) || ahead["behind"] != float64(0) {
		t.Errorf("ahead entry = %v", ahead)
	}
	broken := byName["broken"]
	if broken["verdict"] != "failed" || broken["error_code"] != float64(2) || broken["state"] != "" {
		t.Errorf("broken entry = %v", broken)
	}
	if text, _ := broken["error"].(string); !strings.Contains(text, "config.yaml") {
		t.Errorf("broken error = %q", text)
	}

	summary, ok := doc["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %v", doc["summary"])
	}
	for key, want := range map[string]float64{
		"repos": 3, "current": 1, "ahead": 1, "behind": 0,
		"blocked": 0, "failed": 1, "locked": 0, "skipped": 0,
	} {
		if summary[key] != want {
			t.Errorf("summary[%s] = %v, want %v", key, summary[key], want)
		}
	}
}

func TestStatusRootExitCodeIsWorst(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()

	// Exit code 2: unreadable configuration.
	broken := filepath.Join(root, "broken")
	initRepoAt(t, broken)
	if err := os.WriteFile(filepath.Join(broken, ".git", "doc-connector", "config.yaml"),
		[]byte("remote: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Exit code 4: the remote disappeared after init.
	gone := filepath.Join(root, "gone")
	remote := initRepoAt(t, gone)
	if err := os.RemoveAll(strings.TrimPrefix(remote, "file://")); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, root, "status", "--root", root)
	if code != 4 {
		t.Fatalf("code = %d, want 4 (worst of 2 and 4); out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "summary: current=0 ahead=0 behind=0 blocked=0 failed=2 locked=0") {
		t.Errorf("summary = %q", out)
	}
}

func TestStatusRootSkipsUninitializedAndDepth(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()

	// A checkout without a doc-connector config counts as skipped.
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, plain, "init", "-q", "-b", "main")

	// A repository three levels below the root is out of discovery range.
	deep := filepath.Join(root, "a", "b", "c")
	initRepoAt(t, deep)

	code, out, errOut := run(t, root, "status", "--root", root)
	if code != 0 {
		t.Fatalf("code = %d; err=%q", code, errOut)
	}
	if !strings.Contains(out, "workspace: "+root+" (0 repos, 1 skipped)") {
		t.Errorf("header = %q", out)
	}
}

func TestStatusRootMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	code, out, errOut := run(t, t.TempDir(), "status", "--root", missing)
	if code != 2 {
		t.Fatalf("code = %d, want 2; type out=%q err=%q", code, out, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, missing) {
		t.Errorf("stderr does not name the path: %q", errOut)
	}
	if !strings.Contains(errOut, "hint:") {
		t.Errorf("stderr has no hint: %q", errOut)
	}
}
