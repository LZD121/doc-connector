package cli

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/state"
)

// rewriteMappings replaces the hidden config's docs mappings and registers every
// mapped directory in the managed exclude block, which is what a user editing
// docs_dirs by hand has to do before any command runs again.
func rewriteMappings(t *testing.T, repo string, mappings []config.DocsMapping) {
	t.Helper()
	ctx := context.Background()
	runner := gitstore.New(repo)
	info, err := project.Discover(ctx, runner, repo)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(info.GitDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Schema = config.SchemaVersion
	cfg.DocsDir = ""
	cfg.DocsDirs = mappings
	if err := config.Save(info.GitDir, cfg); err != nil {
		t.Fatal(err)
	}
	dirs := make([]string, 0, len(mappings))
	for _, m := range mappings {
		dirs = append(dirs, m.Path)
	}
	exclude := project.ExcludePath(info.CommonDir)
	if _, err := project.EnsureExcludeDirs(exclude, dirs); err != nil {
		t.Fatal(err)
	}
	if err := project.GuardAllDirs(ctx, gitstore.New(info.WorkTree), dirs, exclude); err != nil {
		t.Fatal(err)
	}
}

// remoteTrackedFiles lists the paths tracked on the private remote.
func remoteTrackedFiles(t *testing.T, url string) []string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "inspect")
	git(t, t.TempDir(), "clone", "-q", url, work)
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(git(t, work, "ls-files")), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	sort.Strings(files)
	return files
}

// TestMultiDocsSync wires two docs directories through init, status, push, pull,
// restore and the unmapped-path guard.
func TestMultiDocsSync(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	mappings := []config.DocsMapping{
		{Path: "private-docs"},
		{Path: "notes", Prefix: "notes"},
	}
	rewriteMappings(t, repo, mappings)

	docs := filepath.Join(repo, "private-docs")
	notes := filepath.Join(repo, "notes")
	for _, dir := range []string{docs, notes} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "b.md"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}

	// status --json lists every mapping and keeps docs_dir as the first path.
	code, out, errOut := run(t, repo, "status", "--json")
	if code != 0 {
		t.Fatalf("status --json: code=%d err=%s", code, errOut)
	}
	document := decodeOne(t, out)
	projectDoc, _ := document["project"].(map[string]any)
	if got := projectDoc["docs_dir"]; got != "private-docs" {
		t.Errorf("project.docs_dir = %v", got)
	}
	listed, _ := projectDoc["docs_dirs"].([]any)
	if len(listed) != 2 {
		t.Fatalf("project.docs_dirs = %v, want two mappings", projectDoc["docs_dirs"])
	}
	first, _ := listed[0].(map[string]any)
	second, _ := listed[1].(map[string]any)
	if first["path"] != "private-docs" || first["prefix"] != "" {
		t.Errorf("docs_dirs[0] = %v", first)
	}
	if second["path"] != "notes" || second["prefix"] != "notes" {
		t.Errorf("docs_dirs[1] = %v", second)
	}

	// push publishes one subtree per mapping: the root one and the prefixed one.
	if code, _, errOut := run(t, repo, "push"); code != 0 {
		t.Fatalf("push: %s", errOut)
	}
	want := []string{pid + "/a.md", pid + "/notes/b.md"}
	if got := remoteTrackedFiles(t, remote); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("remote files = %v, want %v", got, want)
	}
	st, err := state.Load(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	firstCommit := st.SyncedCommit

	// A fresh machine with the same mapping pulls every subtree into the right
	// local directory.
	clone := filepath.Join(t.TempDir(), "machine-b")
	git(t, t.TempDir(), "clone", "-q", "file://"+repo, clone)
	if code, _, errOut := run(t, clone, "init", "--remote", remote); code != 0 {
		t.Fatalf("init clone: %s", errOut)
	}
	rewriteMappings(t, clone, mappings)
	if code, out, errOut := run(t, clone, "pull"); code != 0 {
		t.Fatalf("pull clone: code=%d out=%q err=%q", code, out, errOut)
	}
	wantFiles := map[string]string{
		filepath.Join(clone, "private-docs", "a.md"): "A",
		filepath.Join(clone, "notes", "b.md"):        "B",
	}
	for path, content := range wantFiles {
		data, rerr := os.ReadFile(path)
		if rerr != nil || string(data) != content {
			t.Fatalf("%s = %q, %v", path, data, rerr)
		}
	}

	// restore understands the prefixed project path and writes into notes/.
	if err := os.Remove(filepath.Join(clone, "notes", "b.md")); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = run(t, clone, "restore", "--from", firstCommit, "notes/b.md")
	if code != 0 {
		t.Fatalf("restore: code=%d out=%q err=%q", code, out, errOut)
	}
	if data, rerr := os.ReadFile(filepath.Join(clone, "notes", "b.md")); rerr != nil || string(data) != "B" {
		t.Fatalf("restored notes/b.md = %q, %v", data, rerr)
	}
	if _, rerr := os.Stat(filepath.Join(clone, "private-docs", "notes")); !os.IsNotExist(rerr) {
		t.Error("restore wrote the prefixed path into the root docs directory")
	}

	// A remote path no mapping owns refuses the whole pull before any write. A
	// root mapping (empty prefix) owns every path by definition, so this case
	// needs every mapping to carry a prefix.
	pushRemoteFiles(t, remote, map[string]string{pid + "/unmapped/x.md": "x"}, nil)
	rewriteMappings(t, clone, []config.DocsMapping{
		{Path: "private-docs", Prefix: "docs"},
		{Path: "notes", Prefix: "notes"},
	})
	code, _, errOut = run(t, clone, "pull")
	if code != CodeGuard {
		t.Fatalf("unmapped pull code = %d, want %d; stderr=%q", code, CodeGuard, errOut)
	}
	for _, wantText := range []string{"unmapped/x.md", "docs", "notes"} {
		if !strings.Contains(errOut, wantText) {
			t.Errorf("stderr missing %q: %q", wantText, errOut)
		}
	}
	if _, rerr := os.Stat(filepath.Join(clone, "private-docs", "unmapped", "x.md")); !os.IsNotExist(rerr) {
		t.Error("pull wrote an unmapped remote path")
	}
	// push refuses the same situation: publishing the local view would delete
	// the remote paths no mapping owns.
	if code, _, pushErr := run(t, clone, "push"); code != CodeGuard {
		t.Fatalf("unmapped push code = %d, want %d; stderr=%q", code, CodeGuard, pushErr)
	}
}

// Re-running init on a multi-directory project must keep every mapping in the
// managed exclude block: dropping the others would un-hide those directories.
func TestInitKeepsEveryMapping(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	for _, dir := range []string{"private-docs", "notes"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgFile, _, cerr := config.Load(filepath.Join(repo, ".git"))
	if cerr != nil {
		t.Fatalf("config.Load: %v", cerr)
	}
	cfgFile.DocsDirs = []config.DocsMapping{{Path: "private-docs"}, {Path: "notes", Prefix: "notes"}}
	if err := config.Save(filepath.Join(repo, ".git"), cfgFile); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if _, err := project.EnsureExcludeDirs(project.ExcludePath(filepath.Join(repo, ".git")), []string{"private-docs", "notes"}); err != nil {
		t.Fatalf("EnsureExcludeDirs: %v", err)
	}

	code, _, errOut := run(t, repo, "init", "--remote", remote)
	if code != 0 {
		t.Fatalf("re-init: code=%d err=%s", code, errOut)
	}
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/private-docs/", "/notes/"} {
		if !strings.Contains(string(exclude), want) {
			t.Errorf("exclude block lost %q:\n%s", want, exclude)
		}
	}
	after, _, aerr := config.Load(filepath.Join(repo, ".git"))
	if aerr != nil {
		t.Fatalf("config.Load after re-init: %v", aerr)
	}
	if len(after.DocsDirs) != 2 {
		t.Fatalf("docs_dirs after re-init = %+v", after.DocsDirs)
	}
}
