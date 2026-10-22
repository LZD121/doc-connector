package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/state"
)

// marker writes the discovery marker under dir, creating .git as a directory.
func marker(t *testing.T, dir string) {
	t.Helper()
	cfgDir := filepath.Join(dir, ".git", "doc-connector")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("schema: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()

	alpha := marker0(t, mkdir(t, filepath.Join(root, "alpha")))
	deep := marker0(t, mkdir(t, filepath.Join(root, "beta", "sub")))
	// Depth 2 directory that is a checkout but not initialized.
	if err := os.MkdirAll(filepath.Join(root, "beta", "uninit", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Depth 3 is out of scope even when initialized.
	marker(t, mkdir(t, filepath.Join(root, "gamma", "x", "y")))
	// Dot-prefixed and dependency directories are never descended.
	marker(t, mkdir(t, filepath.Join(root, ".hidden", "repo")))
	marker(t, mkdir(t, filepath.Join(root, "node_modules", "pkg")))
	// A symlink to an initialized repository is not a repository.
	outside := marker0(t, mkdir(t, filepath.Join(t.TempDir(), "outside")))
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Logf("symlinks unavailable: %v", err)
	}
	// Plain files are ignored.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The root itself is a candidate too.
	marker(t, root)

	repos, skipped, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var got []string
	for _, r := range repos {
		got = append(got, r.Path)
	}
	want := []string{root, alpha, deep}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("repos = %v, want %v (sorted by path)", got, want)
	}
	if repos[0].Name != filepath.Base(root) {
		t.Errorf("root name = %q", repos[0].Name)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 (the checkout without config)", skipped)
	}
}

// marker0 marks dir and returns it, for terse table construction.
func marker0(t *testing.T, dir string) string {
	t.Helper()
	marker(t, dir)
	return dir
}

func TestDiscoverMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, _, err := Discover(missing)
	if err == nil {
		t.Fatal("Discover on a missing root returned no error")
	}
	ae, ok := err.(*Error)
	if !ok || ae.Code != 2 {
		t.Fatalf("error = %v (want *Error code 2)", err)
	}
	if !strings.Contains(ae.Hint, missing) {
		t.Errorf("hint %q does not name the path", ae.Hint)
	}
}

// newWorkspaceRepo initializes a doc-connector repository at dir with an empty
// remote, mirroring what "init" would leave behind.
func newWorkspaceRepo(t *testing.T, dir string) string {
	t.Helper()
	mkdir(t, dir)
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "root")

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	git(t, t.TempDir(), "init", "--bare", "-q", "-b", "main", remoteDir)

	ctx := context.Background()
	r := gitstore.New(dir)
	repoInfo, err := project.Discover(ctx, r, dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := project.Resolve(ctx, r, repoInfo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Schema:  config.SchemaVersion,
		Remote:  "file://" + remoteDir,
		Branch:  "main",
		DocsDir: "private-docs",
		Machine: "test",
		Project: config.Project{
			ID: id.ID, IDSource: id.Source, RootCommit: id.RootCommit,
			ObjectFormat: id.ObjectFormat, Adoption: "none",
		},
		Limits:    config.DefaultLimits(),
		CreatedAt: state.Now(),
		CreatedBy: "test",
	}
	if err := config.Save(repoInfo.GitDir, cfg); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(repoInfo.GitDir, state.State{ProjectID: id.ID, Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := project.EnsureExclude(project.ExcludePath(repoInfo.CommonDir), cfg.DocsDir); err != nil {
		t.Fatal(err)
	}
	if err := project.GuardAll(ctx, r, cfg.DocsDir, project.ExcludePath(repoInfo.CommonDir)); err != nil {
		t.Fatal(err)
	}
	// status reads the mirror, so it must exist before the first report.
	store := gitstore.NewStore(gitstore.New(dir), filepath.Join(config.Dir(repoInfo.GitDir), "store.git"))
	if err := store.EnsureBare(ctx, cfg.Branch); err != nil {
		t.Fatal(err)
	}
	if err := store.Fetch(ctx, cfg.Remote); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(dir, cfg.DocsDir))
	return dir
}

func TestDiscoverWorkspaceStatus(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	newWorkspaceRepo(t, filepath.Join(root, "clean"))
	ahead := newWorkspaceRepo(t, filepath.Join(root, "ahead"))
	broken := newWorkspaceRepo(t, filepath.Join(root, "broken"))

	if err := os.WriteFile(filepath.Join(ahead, "private-docs", "a.md"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A corrupt config is a per-repository failure, not a workspace failure.
	if err := os.WriteFile(config.FilePath(filepath.Join(broken, ".git")), []byte("remote: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := WorkspaceStatus(context.Background(),
		Env{Cwd: root, Stdout: io.Discard, Stderr: io.Discard}, root)
	if err != nil {
		t.Fatalf("WorkspaceStatus: %v", err)
	}
	if res.Root != root || len(res.Repos) != 3 {
		t.Fatalf("result = %+v", res)
	}

	byName := map[string]RepoStatus{}
	for _, r := range res.Repos {
		byName[r.Name] = r
	}
	if got := byName["clean"]; got.Verdict != "current" || got.State != "clean" {
		t.Errorf("clean repo = %+v", got)
	}
	if got := byName["ahead"]; got.Verdict != "ahead" || got.State != "ahead" || got.Ahead != 1 {
		t.Errorf("ahead repo = %+v", got)
	}
	if got := byName["broken"]; got.Verdict != "failed" || got.ErrCode != 2 || got.ErrText == "" || got.State != "" {
		t.Errorf("broken repo = %+v", got)
	}

	want := WorkspaceSummary{Repos: 3, Current: 1, Ahead: 1, Failed: 1}
	if res.Summary != want {
		t.Errorf("summary = %+v, want %+v", res.Summary, want)
	}
}

func TestDiscoverVerdicts(t *testing.T) {
	locked := &state.Error{
		Code: 3,
		Msg:  "another doc-connector process holds /x/.git/doc-connector/lock",
		Err:  state.ErrLocked,
	}
	cases := []struct {
		name      string
		state     string
		conflicts int
		err       error
		want      string
	}{
		{"clean", "clean", 0, nil, "current"},
		{"ahead", "ahead", 0, nil, "ahead"},
		{"behind", "behind", 0, nil, "behind"},
		{"diverged", "diverged", 0, nil, "blocked"},
		{"conflicts", "clean", 2, nil, "blocked"},
		{"failed", "", 0, &Error{Code: codeUsage, Msg: "bad config"}, "failed"},
		{"locked", "", 0, locked, "locked"},
	}
	for _, c := range cases {
		if got := verdictFor(c.state, c.conflicts, c.err); got != c.want {
			t.Errorf("%s: verdict = %q, want %q", c.name, got, c.want)
		}
	}
}
