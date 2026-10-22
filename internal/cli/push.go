package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/LZD121/doc-connector/internal/app"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func cmdPush(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "print the plan without writing")
	allowLarge := fs.Bool("allow-large", false, "allow files above the hard size limit")
	var resolve stringList
	fs.Var(&resolve, "resolve", "accept the local version for an unresolved path (repeatable)")

	helped, err := parseFlags(fs, args, e.Stdout, pushUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}

	res, perr := app.Push(ctx, app.Env{
		Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr, Version: e.Version,
	}, app.PushOptions{DryRun: *dryRun, Resolve: resolve, AllowLarge: *allowLarge})
	if res == nil {
		return Wrap(perr, CodeConflict)
	}
	counts := fmt.Sprintf("+%d ~%d -%d (%d tracked)", res.Added, res.Modified, res.Deleted, res.Tracked)
	switch {
	case res.NoOp:
		fmt.Fprintln(e.Stdout, "push: no changes")
	case res.DryRun:
		parent := res.Parent
		if parent == "" {
			parent = "(root)"
		}
		fmt.Fprintf(e.Stdout, "dry-run: would commit tree %s on %s %s\n", res.Tree, shortHex(parent), counts)
	default:
		fmt.Fprintf(e.Stdout, "pushed: %s %s\n", shortHex(res.Commit), counts)
	}
	if len(res.Resolved) > 0 {
		fmt.Fprintf(e.Stdout, "resolved: %s\n", strings.Join(res.Resolved, ", "))
	}
	if perr != nil {
		return Wrap(perr, CodeConflict)
	}
	return nil
}

func shortHex(s string) string {
	if strings.HasPrefix(s, "(") {
		return s
	}
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
