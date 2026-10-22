package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decodeOne decodes exactly one JSON document and fails if a second one
// follows: "one document per invocation" is the contract, not a preference.
func decodeOne(t *testing.T, out string) map[string]any {
	t.Helper()
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("JSON stdout contains ANSI escapes: %q", out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("expected exactly one document, got trailing content: %q", out)
	}
	return doc
}

func TestJSONRequested(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--json"}, true},
		{[]string{"-json"}, true},
		{[]string{"--json=true"}, true},
		{[]string{"--json=false"}, false},
		{[]string{"--offline"}, false},
		{[]string{"--offline", "--json"}, true},
	}
	for _, c := range cases {
		if got := jsonRequested(c.args); got != c.want {
			t.Errorf("jsonRequested(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestStatusJSONDocument(t *testing.T) {
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

	code, out, errOut := run(t, repo, "status", "--json")
	if code != 0 {
		t.Fatalf("status --json: code=%d err=%s", code, errOut)
	}
	doc := decodeOne(t, out)

	if got := doc["schema_version"]; got != float64(1) {
		t.Errorf("schema_version = %v", got)
	}
	if got := doc["command"]; got != "status" {
		t.Errorf("command = %v", got)
	}
	if got := doc["ok"]; got != true {
		t.Errorf("ok = %v (error=%v)", got, doc["error"])
	}
	project, _ := doc["project"].(map[string]any)
	if got := project["id"]; got != pid {
		t.Errorf("project.id = %v, want %s", got, pid)
	}
	if got := project["docs_dir"]; got != "private-docs" {
		t.Errorf("project.docs_dir = %v", got)
	}
	if got := doc["state"]; got != "ahead" {
		t.Errorf("state = %v", got)
	}
	counts, _ := doc["counts"].(map[string]any)
	if got := counts["ahead"]; got != float64(1) {
		t.Errorf("counts.ahead = %v", got)
	}
	files, ok := doc["files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("files = %v", doc["files"])
	}
	file, _ := files[0].(map[string]any)
	if got := file["path"]; got != "a.md" {
		t.Errorf("files[0].path = %v", got)
	}
	if got := file["status"]; got != "A" {
		t.Errorf("files[0].status = %v", got)
	}
	if oid, _ := file["local_oid"].(string); len(oid) < 40 {
		t.Errorf("files[0].local_oid = %q, want a full blob OID", oid)
	}
}

func TestPushAndPullJSONDocument(t *testing.T) {
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

	code, out, errOut := run(t, repo, "push", "--json")
	if code != 0 {
		t.Fatalf("push --json: code=%d err=%s", code, errOut)
	}
	doc := decodeOne(t, out)
	if got := doc["command"]; got != "push" {
		t.Errorf("command = %v", got)
	}
	if got := doc["added"]; got != float64(1) {
		t.Errorf("added = %v", got)
	}
	commit, _ := doc["commit"].(string)
	if len(commit) < 40 {
		t.Errorf("commit = %q, want a full OID", commit)
	}
	if got := doc["no_op"]; got != false {
		t.Errorf("no_op = %v", got)
	}

	code, out, errOut = run(t, repo, "pull", "--json")
	if code != 0 {
		t.Fatalf("pull --json: code=%d err=%s", code, errOut)
	}
	doc = decodeOne(t, out)
	if got := doc["command"]; got != "pull" {
		t.Errorf("command = %v", got)
	}
	if _, ok := doc["applied"].([]any); !ok {
		t.Errorf("applied must be an array, got %v", doc["applied"])
	}
}

func TestJSONFailureIsSingleDocument(t *testing.T) {
	isolateGit(t)
	outside := t.TempDir()

	code, out, _ := run(t, outside, "status", "--json")
	if code == 0 {
		t.Fatalf("status outside a repository must fail, out=%q", out)
	}
	doc := decodeOne(t, out)
	if got := doc["ok"]; got != false {
		t.Errorf("ok = %v", got)
	}
	werr, _ := doc["error"].(map[string]any)
	if werr == nil {
		t.Fatalf("missing error object: %v", doc)
	}
	if got, _ := werr["code"].(string); got == "" || got == "ok" {
		t.Errorf("error.code = %q", got)
	}
	if got, _ := werr["message"].(string); got == "" {
		t.Errorf("error.message must not be empty")
	}
}

func TestHumanOutputUnchangedWithoutJSON(t *testing.T) {
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

	code, out, errOut := run(t, repo, "push")
	if code != 0 {
		t.Fatalf("push: %s", errOut)
	}
	if !strings.HasPrefix(out, "pushed: ") || !strings.Contains(out, "+1 ~0 -0 (1 tracked)") {
		t.Errorf("human push output changed: %q", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("human output must not be JSON: %q", out)
	}
}
