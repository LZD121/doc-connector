package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/LZD121/doc-connector/internal/app"
)

func cmdStatus(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	offline := fs.Bool("offline", false, "do not fetch the remote")

	helped, err := parseFlags(fs, args, e.Stdout, statusUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}

	res, serr := app.Status(ctx, app.Env{Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr}, *offline)
	if res == nil {
		return Wrap(serr, CodeConflict)
	}

	fmt.Fprintf(e.Stdout, "status: %s\n", res.Summary.String())
	changed := false
	for _, line := range app.ActionLines(res.Actions) {
		if strings.TrimSpace(line) != "" {
			changed = true
		}
		fmt.Fprintf(e.Stdout, "  %s\n", line)
	}
	if !changed {
		fmt.Fprintln(e.Stdout, "  (no changes)")
	}
	fmt.Fprintln(e.Stdout, app.RemoteLabel(res))
	fmt.Fprintf(e.Stdout, "ignored: %d\n", res.Ignored)
	if len(res.Skipped) > 0 {
		fmt.Fprintf(e.Stderr, "warning: skipped %d symbolic link(s): %s\n", len(res.Skipped), strings.Join(res.Skipped, ", "))
	}

	if serr != nil {
		return Wrap(serr, CodeRemote)
	}
	return nil
}
