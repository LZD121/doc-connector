package cli

import (
	"strings"
	"testing"
)

// The three scripts are generated from one table, so this test is what stops a
// new command or flag from being offered by bash and forgotten by zsh/fish.
func TestCompletionCoversEveryCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, out, errOut := run(t, "", "completion", shell)
		if code != 0 {
			t.Fatalf("%s: code=%d err=%s", shell, code, errOut)
		}
		for _, cmd := range completionTable {
			if !strings.Contains(out, cmd.Name) {
				t.Errorf("%s script misses command %q", shell, cmd.Name)
			}
			for _, f := range cmd.Flags {
				name := strings.TrimPrefix(f, "--")
				if !strings.Contains(out, name) {
					t.Errorf("%s script misses flag %q of %s", shell, f, cmd.Name)
				}
			}
		}
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	code, out, errOut := run(t, "", "completion", "tcsh")
	if code != CodeUsage {
		t.Fatalf("code = %d, want %d", code, CodeUsage)
	}
	if out != "" {
		t.Errorf("unsupported shell wrote to stdout: %q", out)
	}
	if !strings.Contains(errOut, "hint:") || !strings.Contains(errOut, "bash") {
		t.Errorf("error lacks an actionable hint: %q", errOut)
	}
}

func TestCompletionRequiresShell(t *testing.T) {
	code, _, errOut := run(t, "", "completion")
	if code != CodeUsage || !strings.Contains(errOut, "one shell name") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

// A script that is not valid shell syntax is worse than no completion at all.
func TestCompletionScriptsAreSyntacticallyPlausible(t *testing.T) {
	code, out, _ := run(t, "", "completion", "bash")
	if code != 0 {
		t.Fatalf("bash completion failed")
	}
	if !strings.Contains(out, "complete -F _doc_connector doc-connector") {
		t.Error("bash script does not register the completion function")
	}
	if strings.Count(out, "case \"$cmd\" in") != 1 {
		t.Error("bash script is missing its per-command dispatch")
	}
	code, out, _ = run(t, "", "completion", "zsh")
	if code != 0 || !strings.HasPrefix(out, "#compdef doc-connector") {
		t.Error("zsh script does not start with #compdef")
	}
}
