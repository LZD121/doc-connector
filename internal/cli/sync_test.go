package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newWorkspaceRepo creates an initialized repository under root and returns its
// path plus the private remote URL.
func newWorkspaceRepo(t *testing.T, root, name string) (repo, remote string) {
	t.Helper()
	repo = filepath.Join(root, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("team "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-q", "-m", "init")
	remote = emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init %s: %s", name, errOut)
	}
	return repo, remote
}

func writeDocs(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	docs := filepath.Join(repo, "private-docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		target := filepath.Join(docs, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// syncVerdict returns the verdict line for one repository name.
func syncVerdict(t *testing.T, out, name string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, name+"  ") {
			fields := strings.Fields(line)
			if len(fields) < 6 {
				return ""
			}
			return fields[5]
		}
	}
	return ""
}

func TestSyncRootVerdicts(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()

	cleanRepo, _ := newWorkspaceRepo(t, root, "clean")
	writeDocs(t, cleanRepo, map[string]string{"a.md": "one"})
	if code, _, errOut := run(t, cleanRepo, "push"); code != 0 {
		t.Fatalf("push clean: %s", errOut)
	}

	behindRepo, behindRemote := newWorkspaceRepo(t, root, "behind")
	writeDocs(t, behindRepo, map[string]string{"a.md": "one"})
	if code, _, errOut := run(t, behindRepo, "push"); code != 0 {
		t.Fatalf("push behind: %s", errOut)
	}
	pidBehind := projectID(t, behindRepo)
	pushRemoteFiles(t, behindRemote, map[string]string{pidBehind + "/remote.md": "from elsewhere"}, nil)

	aheadRepo, aheadRemote := newWorkspaceRepo(t, root, "ahead")
	writeDocs(t, aheadRepo, map[string]string{"a.md": "one"})

	divergedRepo, divergedRemote := newWorkspaceRepo(t, root, "diverged")
	writeDocs(t, divergedRepo, map[string]string{"shared.md": "mine"})
	if code, _, errOut := run(t, divergedRepo, "push"); code != 0 {
		t.Fatalf("push diverged: %s", errOut)
	}
	pidDiverged := projectID(t, divergedRepo)
	pushRemoteFiles(t, divergedRemote, map[string]string{pidDiverged + "/shared.md": "theirs"}, nil)
	if err := os.WriteFile(filepath.Join(divergedRepo, "private-docs", "shared.md"), []byte("mine edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, root, "sync", "--root", root)
	if code != CodeConflict {
		t.Fatalf("sync code = %d, want %d\nout=%s\nerr=%s", code, CodeConflict, out, errOut)
	}
	cases := map[string]string{
		"clean":    "current",
		"behind":   "pulled",
		"ahead":    "pushed",
		"diverged": "blocked",
	}
	for name, want := range cases {
		if got := syncVerdict(t, out, name); got != want {
			t.Errorf("%s verdict = %q, want %q\n%s", name, got, want, out)
		}
	}

	// pulled: the remote-only file materialized locally.
	if _, err := os.Stat(filepath.Join(behindRepo, "private-docs", "remote.md")); err != nil {
		t.Errorf("pull did not materialize the remote file: %v", err)
	}
	// pushed: the remote now holds this repository's subtree.
	pidAhead := projectID(t, aheadRepo)
	if got := git(t, remoteDir(aheadRemote), "show", "main:"+pidAhead+"/a.md"); !strings.Contains(got, "one") {
		t.Errorf("ahead was not pushed: %q", got)
	}
	// blocked: the diverged repository is untouched — no conflict copy, no commit.
	if entries, err := os.ReadDir(filepath.Join(divergedRepo, "private-docs")); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), ".theirs.") {
				t.Errorf("sync created a conflict copy: %s", e.Name())
			}
		}
	}
	if body, err := os.ReadFile(filepath.Join(divergedRepo, "private-docs", "shared.md")); err != nil || string(body) != "mine edited" {
		t.Errorf("sync modified the blocked repository: %q %v", body, err)
	}
}

func TestSyncDryRunWritesNothing(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	repo, remote := newWorkspaceRepo(t, root, "ahead")
	writeDocs(t, repo, map[string]string{"a.md": "one"})

	statePath := filepath.Join(repo, ".git", "doc-connector", "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, root, "sync", "--root", root, "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run sync: code=%d out=%s err=%s", code, out, errOut)
	}
	if !strings.Contains(out, "dry-run") || !strings.Contains(out, "would push") {
		t.Errorf("dry-run did not report the pending push: %s", out)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("dry-run advanced the baseline")
	}
	if _, err := os.Stat(filepath.Join(remoteDir(remote), "refs", "heads", "main")); err == nil {
		t.Error("dry-run created the remote branch")
	}
}

// Two projects sharing one private remote must both land: the batch re-fetches
// between repositories instead of pushing concurrently into a lease race.
func TestSyncSharedRemote(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	shared := emptyBareRemote(t)

	var pids []string
	for _, name := range []string{"one", "two"} {
		repo := filepath.Join(root, name)
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, repo, "init", "-q", "-b", "main")
		if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("team "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, repo, "add", "README.md")
		git(t, repo, "commit", "-q", "-m", "init")
		if code, _, errOut := run(t, repo, "init", "--remote", shared); code != 0 {
			t.Fatalf("init %s: %s", name, errOut)
		}
		writeDocs(t, repo, map[string]string{"note.md": name})
		pids = append(pids, projectID(t, repo))
	}

	code, out, errOut := run(t, root, "sync", "--root", root)
	if code != 0 {
		t.Fatalf("sync shared remote: code=%d out=%s err=%s", code, out, errOut)
	}
	for i, pid := range pids {
		name := []string{"one", "two"}[i]
		if got := git(t, remoteDir(shared), "show", "main:"+pid+"/note.md"); !strings.Contains(got, name) {
			t.Errorf("%s not published: %q", name, got)
		}
	}
}

func TestSyncRootJSON(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	repo, _ := newWorkspaceRepo(t, root, "solo")
	writeDocs(t, repo, map[string]string{"a.md": "one"})

	code, out, errOut := run(t, root, "sync", "--root", root, "--json")
	if code != 0 {
		t.Fatalf("sync --json: code=%d err=%s", code, errOut)
	}
	doc := decodeOne(t, out)
	if got := doc["command"]; got != "sync" {
		t.Errorf("command = %v", got)
	}
	repos, ok := doc["repos"].([]any)
	if !ok || len(repos) != 1 {
		t.Fatalf("repos = %v", doc["repos"])
	}
	first, _ := repos[0].(map[string]any)
	if got := first["verdict"]; got != "pushed" {
		t.Errorf("verdict = %v", got)
	}
	summary, _ := doc["summary"].(map[string]any)
	if got := summary["pushed"]; got != float64(1) {
		t.Errorf("summary.pushed = %v", got)
	}
}
