package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/LZD121/doc-connector/internal/app"
)

func cmdPull(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "print the plan without writing")
	force := fs.Bool("force", false, "allow files above the warning size")
	allowLarge := fs.Bool("allow-large", false, "allow files above the hard size limit")
	follow := fs.Bool("follow-symlinks", false, "synchronize symlink targets as regular files")
	jsonOut := fs.Bool("json", false, "print one JSON document")

	helped, err := parseFlags(fs, args, e.Stdout, pullUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}

	res, perr := app.Pull(ctx, app.Env{Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr},
		app.PullOptions{DryRun: *dryRun, Force: *force, AllowLarge: *allowLarge, FollowSymlinks: *follow})
	if res == nil {
		return Wrap(perr, CodeConflict)
	}

	if *jsonOut {
		if err := e.emit(pullDocument(res, perr)); err != nil {
			return err
		}
		if perr != nil {
			return Wrap(perr, CodeConflict)
		}
		return nil
	}

	if res.DryRun {
		fmt.Fprintf(e.Stdout, "dry-run: would write %d file(s), delete %d file(s)\n", res.Writes, res.Deletes)
		for _, line := range app.ActionLines(res.Actions) {
			switch line[0] {
			case 'A', 'M', 'D', 'C':
				fmt.Fprintf(e.Stdout, "  %s\n", line)
			}
		}
	} else if perr == nil {
		fmt.Fprintf(e.Stdout, "pulled: %d written, %d deleted\n", res.Writes, res.Deletes)
	}

	if perr != nil {
		if len(res.Conflicted) > 0 {
			fmt.Fprintf(e.Stderr, "conflict: %d path(s)\n", len(res.Conflicted))
			for _, p := range res.Conflicted {
				fmt.Fprintf(e.Stderr, "  C %s\n", p)
			}
		}
		return Wrap(perr, CodeConflict)
	}
	return nil
}
