package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/LZD121/doc-connector/internal/app"
)

func cmdRestore(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	from := fs.String("from", "", "revision to read (default: remote tip)")
	to := fs.String("to", "", "write into another directory")
	force := fs.Bool("force", false, "overwrite unsynced local changes and clear the conflict")

	helped, err := parseFlags(fs, reorderFlags(args, map[string]bool{"--from": true, "--to": true}), e.Stdout, restoreUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}

	rest := fs.Args()
	if len(rest) > 2 {
		return Errorf(CodeUsage, "restore: too many arguments: %q", strings.Join(rest, " ")).
			WithHint("run \"doc-connector restore --help\" for usage")
	}
	var commit, pathArg string
	switch len(rest) {
	case 2:
		commit, pathArg = rest[0], rest[1]
	case 1:
		pathArg = rest[0]
	default:
		return Errorf(CodeUsage, "restore: expected <commit> <path> or <path>").
			WithHint("run \"doc-connector restore --help\" for usage")
	}

	res, rerr := app.Restore(ctx, app.Env{Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr}, app.RestoreOptions{
		Commit: commit, From: *from, Path: pathArg, To: *to, Force: *force,
	})
	if res == nil {
		return Wrap(rerr, CodeConflict)
	}
	fmt.Fprintf(e.Stdout, "restored: %s at %s\n", res.Path, shortHex(res.Commit))
	if res.Artifact != "" {
		fmt.Fprintf(e.Stdout, "removed conflict artifact: %s\n", res.Artifact)
	}
	if res.Unresolved {
		fmt.Fprintln(e.Stdout, "cleared unresolved conflict (restored the remote version)")
	}
	if rerr != nil {
		return Wrap(rerr, CodeConflict)
	}
	return nil
}

// reorderFlags moves flags (and their values) ahead of positionals so users can
// write `restore <commit> <path> --force`, which stdlib flag does not parse.
func reorderFlags(args []string, takesValue map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if takesValue[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}
