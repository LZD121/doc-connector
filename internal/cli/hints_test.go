package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryErrorCarriesHint is the OBS-03 contract at the boundary users see:
// a failing command must answer "what do I do now". It reads the hint from the
// JSON document because that is the same string the human renderer prints.
func TestEveryErrorCarriesHint(t *testing.T) {
	isolateGit(t)

	t.Run("not initialized", func(t *testing.T) {
		repo := newRepo(t)
		code, out, _ := run(t, repo, "status", "--json")
		assertHint(t, code, out)
	})

	t.Run("unknown revision", func(t *testing.T) {
		repo := newRepo(t)
		remote := emptyBareRemote(t)
		if code, _, errOut := run(t, repo, "init", "--remote", remote); code != 0 {
			t.Fatalf("init: %s", errOut)
		}
		// restore has no --json yet; the human hint line is the same string.
		code, _, stderr := run(t, repo, "restore", "--from", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "a.md")
		if code == 0 {
			t.Fatalf("expected a failure for an unknown revision")
		}
		if !strings.Contains(stderr, "hint:") {
			t.Fatalf("restore error carried no hint: %q", stderr)
		}
	})

	t.Run("resolve on unknown path", func(t *testing.T) {
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
		code, out, _ := run(t, repo, "push", "--json", "--resolve", "nope.md")
		assertHint(t, code, out)
	})
}

func assertHint(t *testing.T, code int, out string) {
	t.Helper()
	if code == 0 {
		t.Fatalf("expected a failure, got exit 0 (out=%q)", out)
	}
	doc := decodeOne(t, out)
	werr, _ := doc["error"].(map[string]any)
	if werr == nil {
		t.Fatalf("missing error object: %q", out)
	}
	hint, _ := werr["hint"].(string)
	if strings.TrimSpace(hint) == "" {
		t.Fatalf("error %v carried no hint: %q", werr["code"], out)
	}
	for _, banned := range []string{"maybe", "possibly", "try again later"} {
		if strings.Contains(strings.ToLower(hint), banned) {
			t.Errorf("hint %q uses speculative wording %q", hint, banned)
		}
	}
}
