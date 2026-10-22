package cli

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LZD121/doc-connector/internal/app"
)

// conflictWorkspace builds an initialized repository whose a.md conflicts:
// local edited, remote edited, pull refused so the conflict is materialized.
func conflictWorkspace(t *testing.T) (root, repo, remote string) {
	t.Helper()
	isolateGit(t)
	root = t.TempDir()
	repo = filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("team proj"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-q", "-m", "init")
	remote = emptyBareRemote(t)
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
	pushRemoteFiles(t, remote, map[string]string{pid + "/a.md": "remote-2"}, nil)
	if code, _, errOut := run(t, repo, "pull"); code != CodeConflict {
		t.Fatalf("conflict pull code = %d, want 1; stderr=%q", code, errOut)
	}
	return root, repo, remote
}

func newTestTUI(t *testing.T, root string, keys string) (*tuiApp, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return &tuiApp{
		ctx:  context.Background(),
		env:  app.Env{Cwd: root, Stdout: out, Stderr: out},
		root: root,
		out:  out,
		keys: bufioReader(keys),
	}, out
}

func TestTuiRefusesNonTTY(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()

	code, out, errOut := run(t, root, "tui", "--root", root)
	if code != CodeUsage {
		t.Fatalf("code = %d, want %d", code, CodeUsage)
	}
	if out != "" {
		t.Errorf("non-TTY TUI wrote to stdout: %q", out)
	}
	if !strings.Contains(errOut, "interactive terminal") || !strings.Contains(errOut, "status --root") {
		t.Errorf("refusal lacks an actionable hint: %q", errOut)
	}
}

func TestTuiRequiresRoot(t *testing.T) {
	code, _, errOut := run(t, "", "tui")
	if code != CodeUsage || !strings.Contains(errOut, "--root") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

// The triage screen must reach the same state as the CLI command it stands for.
func TestTuiKeepLocalMatchesCLI(t *testing.T) {
	root, repo, _ := conflictWorkspace(t)
	tui, _ := newTestTUI(t, root, "\rl")
	if err := tui.run(); err != nil {
		t.Fatalf("tui: %v", err)
	}
	if len(tui.conflict) != 0 {
		t.Fatalf("conflicts remain: %v (status=%q)", tui.conflict, tui.status)
	}
	body, err := os.ReadFile(filepath.Join(repo, "private-docs", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "local-change" {
		t.Errorf("local content lost: %q", body)
	}
	if entries, err := os.ReadDir(filepath.Join(repo, "private-docs")); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), ".theirs.") {
				t.Errorf("TUI left a conflict copy: %s", e.Name())
			}
		}
	}
	// The equivalent CLI answer is a clean status.
	code, out, errOut := run(t, repo, "status")
	if code != 0 || !strings.Contains(out, "status: clean") {
		t.Errorf("status after TUI resolve: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestTuiTakeRemoteRestoresBlob(t *testing.T) {
	root, repo, _ := conflictWorkspace(t)
	tui, out := newTestTUI(t, root, "\rt")
	if err := tui.run(); err != nil {
		t.Fatalf("tui: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, "private-docs", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "remote-2" {
		t.Errorf("remote content not restored: %q", body)
	}
	if len(tui.conflict) != 0 {
		t.Errorf("conflicts remain: %v", tui.conflict)
	}
	if !strings.Contains(out.String(), "took remote") {
		t.Errorf("no status feedback on screen: %q", out.String())
	}
}

func TestTuiSummaryScreenRenders(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	repo, _ := newWorkspaceRepo(t, root, "solo")
	writeDocs(t, repo, map[string]string{"a.md": "one"})

	tui, out := newTestTUI(t, root, "q")
	if err := tui.run(); err != nil {
		t.Fatalf("tui: %v", err)
	}
	screen := out.String()
	if !strings.Contains(screen, "Repositories") || !strings.Contains(screen, "solo") {
		t.Errorf("summary screen = %q", screen)
	}
}

// The TUI must not grow its own git access: everything goes through app.
func TestTuiHasNoDirectGitAccess(t *testing.T) {
	src, err := os.ReadFile("tui.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"gitstore.", "exec.Command", "os/exec"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("tui.go contains %q; TUI actions must go through internal/app", banned)
		}
	}
}

func bufioReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }
