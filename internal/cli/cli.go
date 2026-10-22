package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Env carries process-level dependencies so tests never touch the real
// terminal. Stdout receives results only; Stderr receives diagnostics.
type Env struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	Version string
	Commit  string
	Date    string
	Cwd     string
}

type runnerEnv struct {
	Env
}

// Run parses argv, dispatches one command and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Cwd == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(env.Stderr, "doc-connector: cannot determine working directory: %v\n", err)
			return CodeUsage
		}
		env.Cwd = cwd
	}

	code, err := dispatch(ctx, &runnerEnv{env}, args)
	if err != nil {
		var ce *Error
		if errors.As(err, &ce) {
			fmt.Fprintf(env.Stderr, "doc-connector: %s\n", ce.Msg)
			if ce.Hint != "" {
				fmt.Fprintf(env.Stderr, "hint: %s\n", ce.Hint)
			}
			code = ce.Code
		} else {
			fmt.Fprintf(env.Stderr, "doc-connector: %v\n", err)
			code = CodeConflict
		}
	}

	if ctx.Err() != nil {
		fmt.Fprintln(env.Stderr, "doc-connector: interrupted")
		return CodeInterrupted
	}
	return code
}

func dispatch(ctx context.Context, e *runnerEnv, args []string) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(e.Stderr, rootUsage)
		return CodeUsage, nil
	}

	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(e.Stdout, rootUsage)
		return CodeOK, nil
	case "-version", "--version", "version":
		fmt.Fprintf(e.Stdout, "doc-connector %s\n", versionLabel(e))
		return CodeOK, nil
	}

	name := args[0]
	rest := args[1:]
	switch name {
	case "init":
		return CodeOK, cmdInit(ctx, e, rest)
	case "status":
		return CodeOK, cmdStatus(ctx, e, rest)
	case "push":
		return CodeOK, cmdPush(ctx, e, rest)
	case "pull":
		return CodeOK, cmdPull(ctx, e, rest)
	case "log":
		return CodeOK, stub(e, name, rest)
	case "restore":
		return CodeOK, stub(e, name, rest)
	default:
		if strings.HasPrefix(name, "-") {
			return CodeUsage, Errorf(CodeUsage, "unknown flag %q", name).
				WithHint("run \"doc-connector --help\" for usage")
		}
		return CodeUsage, Errorf(CodeUsage, "unknown command %q", name).
			WithHint("run \"doc-connector --help\" for the command list")
	}
}

func versionLabel(e *runnerEnv) string {
	v := e.Version
	if v == "" {
		v = "dev"
	}
	if e.Commit != "" && e.Commit != "none" {
		return fmt.Sprintf("%s (%s)", v, e.Commit)
	}
	return v
}

// stub reports a documented-but-not-implemented command. It still parses flags
// so --help works and unknown flags fail with a usage error.
func stub(e *runnerEnv, name string, args []string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	helped, err := parseFlags(fs, args, e.Stdout, commandUsage[name])
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	return Errorf(CodeUsage, "%s: not implemented yet", name).
		WithHint("this build only provides the CLI contract; the command lands in a later phase")
}

// parseFlags parses args, printing the command usage to stdout on -h/--help.
func parseFlags(fs *flag.FlagSet, args []string, stdout io.Writer, usage string) (helped bool, err error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return true, nil
		}
		return false, Errorf(CodeUsage, "%s: %v", fs.Name(), err).
			WithHint("run \"doc-connector %s --help\" for usage", fs.Name())
	}
	return false, nil
}

// noPositional rejects positionals for commands that take none.
func noPositional(fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return Errorf(CodeUsage, "%s: unexpected argument %q", fs.Name(), fs.Arg(0)).
			WithHint("run \"doc-connector %s --help\" for usage", fs.Name())
	}
	return nil
}
