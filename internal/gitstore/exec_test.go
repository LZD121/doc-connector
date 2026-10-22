package gitstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		wantErr      bool
	}{
		{"git version 2.47.1", 2, 47, false},
		{"git version 2.39.3 (Apple Git-145)", 2, 39, false},
		{"git version 2.47.1.windows.1", 2, 47, false},
		{"git version 2.30.0\n", 2, 30, false},
		{"git version 1.9.1", 1, 9, false},
		{"not a version", 0, 0, true},
	}
	for _, c := range cases {
		major, minor, _, err := ParseVersion(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseVersion(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.in, err)
		}
		if major != c.major || minor != c.minor {
			t.Errorf("ParseVersion(%q) = %d.%d, want %d.%d", c.in, major, minor, c.major, c.minor)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		stderr string
		want   Kind
	}{
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", KindRemote},
		{"git@github.com: Permission denied (publickey).", KindRemote},
		{"fatal: unable to access 'https://x/': Could not resolve host: x", KindRemote},
		{" ! [rejected]        main -> main (stale info)", KindRace},
		{"error: failed to push some refs to 'x'", KindRace},
		{"fatal: not a git repository", KindOther},
	}
	for _, c := range cases {
		if got := Classify(c.stderr, 128); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.stderr, got, c.want)
		}
	}
}

func TestCleanEnv(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"GIT_DIR=/evil/.git",
		"GIT_WORK_TREE=/evil",
		"GIT_INDEX_FILE=/evil/index",
		"GIT_OBJECT_DIRECTORY=/evil/objects",
		"GIT_COMMON_DIR=/evil/common",
		"GIT_NAMESPACE=x",
		"GIT_CEILING_DIRECTORIES=/evil",
		"GIT_PREFIX=x",
		"GIT_TERMINAL_PROMPT=1",
		"GIT_PAGER=less",
		"LC_ALL=de_DE.UTF-8",
		"GIT_AUTHOR_NAME=Someone",
	}
	got := cleanEnv(base, false)
	joined := strings.Join(got, "\n")
	for _, dropped := range []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=", "GIT_COMMON_DIR=", "GIT_NAMESPACE=", "GIT_CEILING_DIRECTORIES=", "GIT_PREFIX=", "GIT_PAGER=less", "LC_ALL=de_DE"} {
		if strings.Contains(joined, dropped) {
			t.Errorf("cleanEnv kept %q", dropped)
		}
	}
	for _, kept := range []string{"PATH=/usr/bin", "GIT_AUTHOR_NAME=Someone", "LC_ALL=C", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(joined, kept) {
			t.Errorf("cleanEnv missing %q; got:\n%s", kept, joined)
		}
	}
	// Idempotent.
	if second := cleanEnv(got, false); strings.Join(second, "\x00") != strings.Join(got, "\x00") {
		t.Errorf("cleanEnv is not idempotent:\n%v\n%v", got, second)
	}
}

func TestRunnerUsesArgvWithoutShell(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "fake-git")
	body := `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_OUT/args"
env > "$FAKE_OUT/env"
echo "git version 2.47.1"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		Path: script,
		Dir:  dir,
		Env: append(os.Environ(),
			"FAKE_OUT="+out,
			"GIT_DIR=/evil/.git",
		),
	}
	args := []string{"rev-parse", "--show-toplevel", "path with spaces", "$not-expanded"}
	if _, err := r.Run(context.Background(), args...); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(string(got), "\n") != strings.Join(args, "\n") {
		t.Errorf("argv mismatch:\n got %q\nwant %q", got, strings.Join(args, "\n"))
	}
	env, _ := os.ReadFile(filepath.Join(out, "env"))
	for _, bad := range []string{"GIT_DIR=/evil/.git", "GIT_WORK_TREE="} {
		if strings.Contains(string(env), bad) {
			t.Errorf("child environment leaked %q", bad)
		}
	}
	for _, want := range []string{"LC_ALL=C", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("child environment missing %q", want)
		}
	}
}

func TestRunnerErrorCarriesStderrAndExitCode(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'fatal: not a git repository' >&2\nexit 128\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Path: script, Dir: dir, Env: os.Environ()}
	_, err := r.Run(context.Background(), "status")
	ge, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if ge.Code != 128 {
		t.Errorf("exit code = %d, want 128", ge.Code)
	}
	if !strings.Contains(ge.Stderr, "not a git repository") {
		t.Errorf("stderr not captured: %q", ge.Stderr)
	}
	if ge.ExitCode() != 1 {
		t.Errorf("classified exit = %d, want 1", ge.ExitCode())
	}
}
