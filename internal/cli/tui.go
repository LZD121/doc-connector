package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/LZD121/doc-connector/internal/app"
)

const tuiUsage = `Usage:
  doc-connector tui --root <dir>

Opens an interactive screen over every initialized repository under <dir>:
pick a repository, see its conflicting paths, then keep the local version or
take the remote one. Every action runs the same app functions as the CLI —
the TUI never talks to git itself.

Keys: up/down move, enter open, esc back, q quit.

Flags:
      --root <dir>   Directory that contains the repositories (required)
  -h, --help         Show this help
`

// tuiApp is the whole interactive state. It is a plain struct with an injected
// reader and writer, so tests drive it with a key script instead of a terminal.
type tuiApp struct {
	ctx  context.Context
	env  app.Env
	root string
	out  io.Writer
	keys *bufio.Reader

	repos    []app.RepoStatus
	cursor   int
	screen   int // screenSummary or screenConflicts
	current  app.RepoStatus
	conflict []string
	cfCursor int
	status   string
	quit     bool
}

const (
	screenSummary = iota
	screenConflicts
)

func cmdTui(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "directory that contains the repositories")

	helped, err := parseFlags(fs, args, e.Stdout, tuiUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}
	if *root == "" {
		return Errorf(CodeUsage, "tui: --root <dir> is required").
			WithHint("pass the directory that contains your repositories, for example --root ~/repos")
	}
	if !interactiveStdio(e) {
		return Errorf(CodeUsage, "tui needs an interactive terminal").
			WithHint("use \"doc-connector status --root <dir>\" when output is piped or stdin is not a terminal")
	}

	app2 := &tuiApp{
		ctx:  ctx,
		env:  app.Env{Cwd: e.Cwd, Stdout: io.Discard, Stderr: e.Stderr},
		root: *root,
		out:  e.Stdout,
		keys: bufio.NewReader(e.Stdin),
	}

	var restore func()
	if f, ok := e.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if state, terr := term.MakeRaw(int(f.Fd())); terr == nil {
			restore = func() { _ = term.Restore(int(f.Fd()), state) }
		}
	}
	if restore != nil {
		defer restore()
	}
	fmt.Fprint(e.Stdout, "\x1b[?1049h") // alternate screen
	defer fmt.Fprint(e.Stdout, "\x1b[?1049l")

	return app2.run()
}

// interactiveStdio is the non-TTY refusal gate: a TUI cannot run when nobody
// can see it or press a key.
func interactiveStdio(e *runnerEnv) bool {
	in, ok := e.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return false
	}
	out, ok := e.Stdout.(*os.File)
	if !ok || !term.IsTerminal(int(out.Fd())) {
		return false
	}
	return true
}

func (t *tuiApp) run() error {
	if err := t.refresh(); err != nil {
		return err
	}
	for !t.quit {
		t.draw()
		key, err := t.readKey()
		if err != nil {
			return nil // EOF: treat like quit
		}
		if err := t.handle(key); err != nil {
			return err
		}
	}
	return nil
}

// refresh reloads the repository list through the same aggregate call the CLI
// uses, so the TUI can never disagree with `status --root`.
func (t *tuiApp) refresh() error {
	res, err := app.WorkspaceStatus(t.ctx, t.env, t.root)
	if err != nil {
		return Wrap(err, CodeUsage)
	}
	t.repos = res.Repos
	if t.cursor >= len(t.repos) {
		t.cursor = max(0, len(t.repos)-1)
	}
	return nil
}

func (t *tuiApp) readKey() (string, error) {
	b, err := t.keys.ReadByte()
	if err != nil {
		return "", err
	}
	if b == '\r' || b == '\n' {
		return "enter", nil
	}
	if b != 0x1b {
		return string(b), nil
	}
	if t.keys.Buffered() == 0 {
		return "esc", nil
	}
	next, err := t.keys.ReadByte()
	if err != nil {
		return "esc", nil
	}
	if next != '[' {
		return "esc", nil
	}
	code, err := t.keys.ReadByte()
	if err != nil {
		return "esc", nil
	}
	switch code {
	case 'A':
		return "up", nil
	case 'B':
		return "down", nil
	default:
		return "", nil
	}
}

func (t *tuiApp) handle(key string) error {
	switch key {
	case "q":
		t.quit = true
		return nil
	case "up", "k":
		if t.screen == screenSummary {
			if t.cursor > 0 {
				t.cursor--
			}
			return nil
		}
		if t.cfCursor > 0 {
			t.cfCursor--
		}
		return nil
	case "down", "j":
		if t.screen == screenSummary {
			if t.cursor < len(t.repos)-1 {
				t.cursor++
			}
			return nil
		}
		if t.cfCursor < len(t.conflict)-1 {
			t.cfCursor++
		}
		return nil
	case "enter":
		if t.screen == screenSummary && len(t.repos) > 0 {
			return t.openConflicts()
		}
		return nil
	case "esc":
		if t.screen == screenConflicts {
			t.screen = screenSummary
			t.status = ""
			return t.refresh()
		}
		t.quit = true
		return nil
	case "r":
		return t.refresh()
	case "l":
		return t.resolve("local")
	case "t":
		return t.resolve("theirs")
	}
	return nil
}

// openConflicts enters the triage screen for the selected repository.
func (t *tuiApp) openConflicts() error {
	t.current = t.repos[t.cursor]
	t.screen = screenConflicts
	t.cfCursor = 0
	t.status = ""
	return t.reloadConflicts()
}

func (t *tuiApp) reloadConflicts() error {
	st, err := app.Status(t.ctx, t.repoEnv(), app.StatusOptions{})
	if err != nil && st == nil {
		t.conflict = nil
		t.status = "status failed: " + err.Error()
		return nil
	}
	t.conflict = app.ConflictPaths(st.Actions)
	if len(t.conflict) == 0 && t.status == "" {
		t.status = "no conflicts in this repository"
	}
	return nil
}

func (t *tuiApp) repoEnv() app.Env {
	return app.Env{Cwd: t.current.Path, Stdout: io.Discard, Stderr: t.env.Stderr}
}

// resolve is the only place the TUI changes anything, and it does so by calling
// the CLI's own app commands: local → push --resolve, theirs → restore --force.
func (t *tuiApp) resolve(side string) error {
	if t.screen != screenConflicts || len(t.conflict) == 0 {
		return nil
	}
	path := t.conflict[t.cfCursor]
	switch side {
	case "local":
		if _, err := app.Push(t.ctx, t.repoEnv(), app.PushOptions{Resolve: []string{path}}); err != nil {
			t.status = fmt.Sprintf("keep local failed: %v", err)
			return t.reloadConflicts()
		}
		t.status = "kept local: " + path
	case "theirs":
		if _, err := app.Restore(t.ctx, t.repoEnv(), app.RestoreOptions{Path: path, Force: true}); err != nil {
			t.status = fmt.Sprintf("take remote failed: %v", err)
			return t.reloadConflicts()
		}
		t.status = "took remote: " + path
	}
	if err := t.reloadConflicts(); err != nil {
		return err
	}
	return t.refresh()
}

func (t *tuiApp) draw() {
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	b.WriteString("doc-connector tui  —  " + t.root + "\n\n")
	if t.screen == screenSummary {
		b.WriteString("Repositories (enter: conflicts, r: refresh, q: quit)\n\n")
		for i, repo := range t.repos {
			line := fmt.Sprintf("  %-16s %-10s %-10s %s", repo.Name, orDash(repo.ProjectID), orDash(repo.State), repo.Verdict)
			if i == t.cursor {
				line = "\x1b[7m" + line + "\x1b[0m"
			}
			b.WriteString(line + "\n")
		}
		if len(t.repos) == 0 {
			b.WriteString("  (no initialized repository found)\n")
		}
	} else {
		fmt.Fprintf(&b, "%s — conflicts (l: keep local, t: take remote, esc: back)\n\n", t.current.Name)
		if len(t.conflict) == 0 {
			b.WriteString("  (none)\n")
		}
		for i, path := range t.conflict {
			line := "  C " + path
			if i == t.cfCursor {
				line = "\x1b[7m" + line + "\x1b[0m"
			}
			b.WriteString(line + "\n")
		}
	}
	if t.status != "" {
		b.WriteString("\n" + t.status + "\n")
	}
	fmt.Fprint(t.out, b.String())
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
