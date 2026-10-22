// Package gitstore is the only place in the program that executes git.
//
// Every call uses an argument slice with exec.CommandContext (never a shell
// string), an explicit working directory, and a scrubbed environment. Callers
// receive classified errors and never parse git output themselves.
package gitstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Kind classifies a git failure so the application layer can map it to the
// closed exit-code set without matching strings itself.
type Kind int

const (
	KindOther Kind = iota
	KindRemote
	KindRace
	KindUnsupportedVersion
)

// Error is a failed git invocation.
type Error struct {
	Args   []string
	Code   int // process exit code, or -1 when git could not start
	Stderr string
	Kind   Kind
	Err    error
}

func (e *Error) Error() string {
	detail := strings.TrimSpace(e.Stderr)
	if detail == "" && e.Err != nil {
		detail = e.Err.Error()
	}
	if detail == "" {
		detail = fmt.Sprintf("git exited with status %d", e.Code)
	}
	if e.Kind == KindUnsupportedVersion {
		return detail
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), detail)
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode maps the failure onto the CLI contract: network/authentication is
// 4, an unsupported git version is 2, everything else is 1.
func (e *Error) ExitCode() int {
	switch e.Kind {
	case KindRemote:
		return 4
	case KindUnsupportedVersion:
		return 2
	default:
		return 1
	}
}

// HintText returns a stable, actionable next step.
func (e *Error) HintText() string {
	switch e.Kind {
	case KindRemote:
		return "check network access and credentials (SSH agent or credential helper); doc-connector never prompts"
	case KindUnsupportedVersion:
		return "install git 2.30 or newer"
	case KindRace:
		return "the remote moved while pushing; re-run after pulling"
	default:
		return ""
	}
}

// Runner executes git commands for one project directory.
type Runner struct {
	// Path is the git executable; empty means "git" from PATH.
	Path string
	// Dir is the working directory for every command.
	Dir string
	// Env is the base environment (normally os.Environ()).
	Env []string
	// Interactive is reserved: doc-connector never prompts today, so
	// GIT_TERMINAL_PROMPT is always 0.
	Interactive bool
}

// New returns a Runner rooted at dir using the process environment.
func New(dir string) *Runner {
	return &Runner{Path: "git", Dir: dir, Env: os.Environ()}
}

func (r *Runner) path() string {
	if r.Path == "" {
		return "git"
	}
	return r.Path
}

// Cmd builds a git command with the scrubbed environment. Callers that need
// streaming stdin/stdout (hash-object, cat-file --batch) use this directly.
func (r *Runner) Cmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.path(), args...)
	cmd.Dir = r.Dir
	cmd.Env = cleanEnv(r.Env, r.Interactive)
	return cmd
}

// Run executes git and returns stdout. stderr is only surfaced on failure.
func (r *Runner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := r.Cmd(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return stdout.String(), ctx.Err()
		}
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		stderrText := stderr.String()
		return stdout.String(), &Error{
			Args:   args,
			Code:   code,
			Stderr: stderrText,
			Kind:   Classify(stderrText, code),
			Err:    err,
		}
	}
	return stdout.String(), nil
}

// gitEnvDrops are the variables that let a parent git process (hook, IDE,
// sibling tool) redirect us to a different repository. They are removed so an
// explicit --git-dir and Dir always win.
var gitEnvDrops = map[string]struct{}{
	"GIT_DIR":                          {},
	"GIT_WORK_TREE":                    {},
	"GIT_INDEX_FILE":                   {},
	"GIT_OBJECT_DIRECTORY":             {},
	"GIT_COMMON_DIR":                   {},
	"GIT_NAMESPACE":                    {},
	"GIT_CEILING_DIRECTORIES":          {},
	"GIT_PREFIX":                       {},
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_QUARANTINE_PATH":              {},
}

// cleanEnv removes repository-locating variables and pins locale so diagnosis
// and porcelain parsing are stable. It is idempotent.
func cleanEnv(base []string, interactive bool) []string {
	out := make([]string, 0, len(base)+4)
	for _, kv := range base {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, drop := gitEnvDrops[key]; drop {
			continue
		}
		// Never inherit an inherited prompt policy either; we set it below.
		if key == "GIT_TERMINAL_PROMPT" || key == "GIT_PAGER" || key == "LC_ALL" || key == "LANG" {
			continue
		}
		out = append(out, kv)
	}
	prompt := "0"
	if interactive {
		prompt = "1"
	}
	out = append(out,
		"LC_ALL=C",
		"LANG=C",
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT="+prompt,
	)
	return out
}
