package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/LZD121/doc-connector/internal/app"
)

func cmdLog(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pathArg := fs.String("path", "", "only commits touching this path")
	limit := fs.Int("limit", 20, "maximum commits to show")

	helped, err := parseFlags(fs, args, e.Stdout, logUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}

	res, lerr := app.Log(ctx, app.Env{Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr}, app.LogOptions{
		Path:  *pathArg,
		Limit: *limit,
	})
	if res == nil {
		return Wrap(lerr, CodeConflict)
	}
	if len(res.Entries) == 0 {
		fmt.Fprintln(e.Stdout, "log: no commits")
	} else {
		for _, entry := range res.Entries {
			machine := entry.Machine
			if machine == "" {
				machine = "-"
			}
			fmt.Fprintf(e.Stdout, "%s  %s  %-12s  %s\n", shortHex(entry.OID), entry.At, machine, entry.Subject)
		}
	}
	if age := app.FetchedAge(res.Fetched); age != "" {
		fmt.Fprintf(e.Stdout, "fetched: %s\n", age)
	}
	if lerr != nil {
		return Wrap(lerr, CodeConflict)
	}
	return nil
}
