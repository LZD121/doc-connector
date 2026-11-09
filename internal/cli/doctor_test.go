package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorCleanProject(t *testing.T) {
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
	if code, _, errOut := run(t, repo, "push"); code != 0 {
		t.Fatalf("push: %s", errOut)
	}

	code, out, errOut := run(t, repo, "doctor")
	if code != 0 {
		t.Fatalf("doctor on a clean project: code=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "[ok] git-version") || !strings.Contains(out, "summary:") {
		t.Errorf("doctor report = %q", out)
	}
	if strings.Contains(out, "[fail]") {
		t.Errorf("clean project reported a failure: %q", out)
	}
}

func TestDoctorJSONDocument(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}

	code, out, errOut := run(t, repo, "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor --json: code=%d err=%s", code, errOut)
	}
	doc := decodeOne(t, out)
	if got := doc["command"]; got != "doctor" {
		t.Errorf("command = %v", got)
	}
	checks, ok := doc["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("checks = %v", doc["checks"])
	}
	first, _ := checks[0].(map[string]any)
	if got := first["id"]; got != "git-version" {
		t.Errorf("checks[0].id = %v", got)
	}
	summary, _ := doc["summary"].(map[string]any)
	if _, ok := summary["fail"]; !ok {
		t.Errorf("summary missing fail count: %v", summary)
	}
}

func TestDoctorReportsTamperedExclude(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}

	exclude := filepath.Join(repo, ".git", "info", "exclude")
	data, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), "/private-docs/", "/other-docs/", 1)
	if err := os.WriteFile(exclude, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, _ := run(t, repo, "doctor")
	if code != CodeGuard {
		t.Fatalf("doctor after tampering: code=%d, want %d (out=%q)", code, CodeGuard, out)
	}
	if !strings.Contains(out, "[fail] exclude") {
		t.Errorf("doctor did not flag the exclude block: %q", out)
	}
	if !strings.Contains(out, canonical(t, exclude)) {
		t.Errorf("doctor detail must name the exclude path: %q", out)
	}
}

// canonical resolves the aliases a temporary directory may carry: macOS hands
// out /var paths that git reports as /private/var, Windows hands out 8.3 names
// that git reports expanded. The report names the file itself, not a spelling.
func canonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}
