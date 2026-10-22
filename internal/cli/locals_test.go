package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// remoteTree lists <branch>:<root> of the private repository as path -> mode.
func remoteTree(t *testing.T, url, branch string) map[string]string {
	t.Helper()
	out := git(t, remoteDir(url), "ls-tree", "-r", branch)
	tree := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		meta, pathName, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("unexpected ls-tree line: %q", line)
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			t.Fatalf("unexpected ls-tree meta: %q", line)
		}
		tree[pathName] = fields[0]
	}
	return tree
}

func TestDocIgnoreEndToEnd(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t)
	remote := emptyBareRemote(t)
	if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	pid := projectID(t, repo)
	docs := filepath.Join(repo, "private-docs")
	for _, dir := range []string{"notes", "drafts"} {
		if err := os.MkdirAll(filepath.Join(docs, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"a.md":             "one",
		"notes/keep.md":    "keep",
		"notes/secret.tmp": "scratch",
		"drafts/wip.md":    "draft",
		".docignore":       "*.tmp\ndrafts/\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(docs, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	code, out, errOut := run(t, repo, "status")
	if code != 0 {
		t.Fatalf("status: code=%d err=%q", code, errOut)
	}
	for _, want := range []string{"a.md", "notes/keep.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("status does not list %q:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"secret.tmp", "drafts", ".docignore", "wip.md"} {
		if strings.Contains(out, hidden) {
			t.Errorf("ignored path %q appears in status:\n%s", hidden, out)
		}
	}

	code, out, errOut = run(t, repo, "push")
	if code != 0 {
		t.Fatalf("push: code=%d out=%q err=%q", code, out, errOut)
	}
	tree := remoteTree(t, remote, "main")
	want := map[string]bool{pid + "/a.md": true, pid + "/notes/keep.md": true}
	for pathName := range want {
		if _, ok := tree[pathName]; !ok {
			t.Errorf("pushed tree is missing %q: %v", pathName, tree)
		}
	}
	if len(tree) != len(want) {
		t.Errorf("pushed tree has unexpected entries: %v", tree)
	}
	for pathName := range tree {
		for _, hidden := range []string{"secret.tmp", "drafts", ".docignore", "wip.md"} {
			if strings.Contains(pathName, hidden) {
				t.Errorf("ignored path %q reached the remote tree", pathName)
			}
		}
	}

	// A path becomes ignored locally after it was already synced. A pull must
	// not write it (not even a .theirs copy) or turn it into a conflict.
	if err := os.WriteFile(filepath.Join(docs, ".docignore"), []byte("*.tmp\ndrafts/\nkeep.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pushRemoteFiles(t, remote, map[string]string{pid + "/notes/keep.md": "remote edit"}, nil)

	code, out, errOut = run(t, repo, "pull")
	if code != 0 {
		t.Fatalf("pull over an ignored path: code=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "0 written, 0 deleted") {
		t.Errorf("pull touched an ignored path: %q", out)
	}
	if data, err := os.ReadFile(filepath.Join(docs, "notes", "keep.md")); err != nil || string(data) != "keep" {
		t.Errorf("ignored local file changed: %q %v", data, err)
	}
	artifacts, err := filepath.Glob(filepath.Join(docs, "notes", "keep.md.theirs.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Errorf("pull wrote conflict artifacts for an ignored path: %v", artifacts)
	}
}

func TestFollowSymlinksEndToEnd(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(docs, "real.md"), []byte("target-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.md", filepath.Join(docs, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Default behavior is v1.0: the link is reported and never synchronized.
	code, out, errOut := run(t, repo, "status")
	if code != 0 {
		t.Fatalf("status: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "skipped 1 symbolic link(s): link.md") {
		t.Errorf("default status did not report the link: %q", errOut)
	}
	if strings.Contains(out, "link.md") {
		t.Errorf("skipped link appears in status actions:\n%s", out)
	}

	code, _, errOut = run(t, repo, "push")
	if code != 0 {
		t.Fatalf("push without follow: %s", errOut)
	}
	if _, ok := remoteTree(t, remote, "main")[pid+"/link.md"]; ok {
		t.Fatal("link was pushed without --follow-symlinks")
	}

	code, out, errOut = run(t, repo, "push", "--follow-symlinks")
	if code != 0 {
		t.Fatalf("push --follow-symlinks: code=%d out=%q err=%q", code, out, errOut)
	}
	tree := remoteTree(t, remote, "main")
	mode, ok := tree[pid+"/link.md"]
	if !ok {
		t.Fatalf("link missing from the pushed tree: %v", tree)
	}
	if mode != "100644" {
		t.Errorf("link mode = %s, want 100644", mode)
	}
	blob := git(t, remoteDir(remote), "cat-file", "-p", "main:"+pid+"/link.md")
	if blob != "target-bytes" {
		t.Errorf("link blob = %q, want the target bytes", blob)
	}

	// With the flag the link is no longer reported as skipped.
	code, out, errOut = run(t, repo, "status", "--follow-symlinks")
	if code != 0 {
		t.Fatalf("status --follow-symlinks: code=%d out=%q err=%q", code, out, errOut)
	}
	if strings.Contains(errOut, "symbolic link") {
		t.Errorf("followed link still reported as skipped: %q", errOut)
	}
	if !strings.Contains(out, "status: clean") {
		t.Errorf("link bytes not recognized as synced: %q", out)
	}

	// A target outside the docs root refuses the whole operation with code 3.
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(docs, "escape.md")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = run(t, repo, "status", "--follow-symlinks")
	if code != 3 {
		t.Fatalf("escaping symlink: code=%d, want 3 (stderr=%q)", code, errOut)
	}
	if !strings.Contains(errOut, "escape.md") {
		t.Errorf("refusal does not name the offending link: %q", errOut)
	}
}
