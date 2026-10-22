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
	"github.com/LZD121/doc-connector/internal/state"
)

// pushDocs pushes the docs directory and returns the new synced commit.
func pushDocs(t *testing.T, repo string) string {
	t.Helper()
	env := Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}
	res, err := Push(context.Background(), env, PushOptions{})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if res.Commit == "" {
		t.Fatal("push did not report a commit")
	}
	return res.Commit
}

func writeDoc(t *testing.T, docs, name, content string) {
	t.Helper()
	full := filepath.Join(docs, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func diffFiles(res *DiffResult) map[string]DiffFile {
	files := map[string]DiffFile{}
	for _, f := range res.Files {
		files[f.Path] = f
	}
	return files
}

func TestDiffHistory(t *testing.T) {
	rc, repo := setupProject(t)
	if err := rc.Store.EnsureBare(context.Background(), rc.Config.Branch); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(repo, "private-docs")

	writeDoc(t, docs, "a.md", "one\ntwo\n")
	writeDoc(t, docs, "b.md", "gone\n")
	writeDoc(t, docs, "c.md", "keep\n")
	first := pushDocs(t, repo)

	writeDoc(t, docs, "a.md", "one\nTWO\n")
	if err := os.Remove(filepath.Join(docs, "b.md")); err != nil {
		t.Fatal(err)
	}
	second := pushDocs(t, repo)

	ctx := context.Background()
	env := Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}
	res, err := Diff(ctx, env, DiffOptions{Commit: first})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if res.From != first || res.To != second {
		t.Errorf("from/to = %s/%s, want %s/%s", res.From, res.To, first, second)
	}
	if !strings.Contains(res.Raw, "diff --git a/a.md b/a.md") {
		t.Errorf("patch header missing:\n%s", res.Raw)
	}
	if !strings.Contains(res.Raw, "-two") || !strings.Contains(res.Raw, "+TWO") {
		t.Errorf("modified lines missing:\n%s", res.Raw)
	}

	files := diffFiles(res)
	if f, ok := files["a.md"]; !ok || f.Status != "M" || f.Insertions != 1 || f.Deletions != 1 {
		t.Errorf("a.md = %+v (present %v)", f, ok)
	}
	if f, ok := files["b.md"]; !ok || f.Status != "D" || f.Deletions != 1 || f.Insertions != 0 {
		t.Errorf("b.md = %+v (present %v)", f, ok)
	}
	if _, ok := files["c.md"]; ok {
		t.Error("unchanged c.md appeared in the diff")
	}

	// A path argument narrows the patch to that subtree path.
	narrow, err := Diff(ctx, env, DiffOptions{Commit: first, Path: "a.md"})
	if err != nil {
		t.Fatalf("diff path: %v", err)
	}
	if len(narrow.Files) != 1 || narrow.Files[0].Path != "a.md" {
		t.Errorf("narrowed files = %+v", narrow.Files)
	}
	if strings.Contains(narrow.Raw, "b.md") {
		t.Errorf("b.md leaked through the path filter:\n%s", narrow.Raw)
	}

	// The remote is broken: diff must still work from the mirror.
	cfg := *rc.Config
	cfg.Remote = "file://" + filepath.Join(t.TempDir(), "gone.git")
	if err := config.Save(rc.Repo.GitDir, &cfg); err != nil {
		t.Fatal(err)
	}
	offline, err := Diff(ctx, env, DiffOptions{Commit: first})
	if err != nil {
		t.Fatalf("offline diff: %v", err)
	}
	if len(offline.Files) != 2 {
		t.Errorf("offline files = %d, want 2", len(offline.Files))
	}
	if before, _ := state.Load(rc.Repo.GitDir); before.SyncedCommit != second {
		t.Errorf("diff advanced the baseline: %s", before.SyncedCommit)
	}

	// A revision that predates the project prefix diffs against the empty tree,
	// so the whole prefix reads as added instead of failing on a missing tree.
	emptyTree, err := rc.Store.Run(ctx, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		t.Fatalf("empty tree: %v", err)
	}
	pre, err := rc.Store.CommitTree(ctx, strings.TrimSpace(emptyTree), "", "before docs",
		gitstore.Signature{Name: "Test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("pre-docs commit: %v", err)
	}
	added, err := Diff(ctx, env, DiffOptions{Commit: pre})
	if err != nil {
		t.Fatalf("diff before prefix: %v", err)
	}
	if len(added.Files) != 2 {
		t.Fatalf("added files = %+v", added.Files)
	}
	for _, f := range added.Files {
		if f.Status != "A" || !strings.Contains(f.Patch, "new file mode") {
			t.Errorf("%s = %+v, want an addition", f.Path, f)
		}
	}

	// The default base is the mirror tip, so it compares equal to itself.
	same, err := Diff(ctx, env, DiffOptions{})
	if err != nil {
		t.Fatalf("default diff: %v", err)
	}
	if same.Raw != "" || len(same.Files) != 0 {
		t.Errorf("default diff not empty: %+v", same)
	}
}

func TestDiffUnknownRevision(t *testing.T) {
	rc, repo := setupProject(t)
	if err := rc.Store.EnsureBare(context.Background(), rc.Config.Branch); err != nil {
		t.Fatal(err)
	}
	env := Env{Cwd: repo, Stdout: io.Discard, Stderr: io.Discard}

	res, err := Diff(context.Background(), env, DiffOptions{Commit: "no-such-revision"})
	if res != nil {
		t.Fatalf("expected no result, got %+v", res)
	}
	ae, ok := err.(*Error)
	if !ok {
		t.Fatalf("error = %T %v", err, err)
	}
	if ae.ExitCode() != codeUsage {
		t.Errorf("exit = %d, want %d", ae.ExitCode(), codeUsage)
	}
	if ae.HintText() == "" {
		t.Error("unknown revision must carry a hint")
	}
}

func TestDiffParsesBinaryAndTruncates(t *testing.T) {
	raw := "diff --git a/bin.dat b/bin.dat\n" +
		"index 8352675..c5793f9 100644\n" +
		"Binary files a/bin.dat and b/bin.dat differ\n"
	files := parseDiff(raw)
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	if files[0].Path != "bin.dat" || files[0].Patch != "Binary files a/bin.dat and b/bin.dat differ\n" {
		t.Errorf("binary file = %+v", files[0])
	}

	big := "diff --git a/big.md b/big.md\n--- a/big.md\n+++ b/big.md\n@@ -1 +1 @@\n" +
		"+" + strings.Repeat("x", maxPatchBytes+10) + "\n"
	files = parseDiff(big)
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	if !files[0].Truncated {
		t.Error("large patch was not truncated")
	}
	if len(files[0].Patch) > maxPatchBytes {
		t.Errorf("patch length = %d, cap %d", len(files[0].Patch), maxPatchBytes)
	}
	if files[0].Insertions != 1 {
		t.Errorf("insertions = %d", files[0].Insertions)
	}
}
