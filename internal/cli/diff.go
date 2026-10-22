package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/LZD121/doc-connector/internal/app"
)

const diffUsage = `Usage:
  doc-connector diff <commit> [path] [flags]
  doc-connector diff --from <commit> [path] [flags]

Shows how one historical version of this project's prefix differs from the
currently synced revision. Reads the local mirror only: never fetches, never
writes state.

Flags:
      --from <commit>  Revision to compare from (default: the remote tip)
      --json           Print one JSON document
  -h, --help           Show this help
`

func cmdDiff(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	from := fs.String("from", "", "revision to compare from")
	jsonOut := fs.Bool("json", false, "print one JSON document")

	helped, err := parseFlags(fs, reorderFlags(args, map[string]bool{"--from": true}), e.Stdout, diffUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}

	rest := fs.Args()
	if len(rest) > 2 {
		return Errorf(CodeUsage, "diff: too many arguments: %q", strings.Join(rest, " ")).
			WithHint("run \"doc-connector diff --help\" for usage")
	}
	// `diff <commit> <path>` names both; a lone argument is the commit unless
	// --from already supplied the revision, in which case it is the path.
	var commit, pathArg string
	switch {
	case len(rest) == 2:
		commit, pathArg = rest[0], rest[1]
	case len(rest) == 1 && *from != "":
		pathArg = rest[0]
	case len(rest) == 1:
		commit = rest[0]
	}

	res, derr := app.Diff(ctx, app.Env{Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr}, app.DiffOptions{
		Commit: commit, From: *from, Path: pathArg,
	})
	if res == nil {
		return Wrap(derr, CodeConflict)
	}

	if *jsonOut {
		if err := e.emit(diffDocument(res, derr)); err != nil {
			return err
		}
		if derr != nil {
			return Wrap(derr, CodeConflict)
		}
		return nil
	}

	if res.Raw == "" {
		fmt.Fprintln(e.Stdout, "no differences")
	} else {
		fmt.Fprint(e.Stdout, res.Raw)
		if !strings.HasSuffix(res.Raw, "\n") {
			fmt.Fprintln(e.Stdout)
		}
	}
	if derr != nil {
		return Wrap(derr, CodeConflict)
	}
	return nil
}

type diffFileDoc struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	Patch      string `json:"patch"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type diffDoc struct {
	Envelope
	From  string        `json:"from"`
	To    string        `json:"to"`
	Path  string        `json:"path,omitempty"`
	Files []diffFileDoc `json:"files"`
}

func diffDocument(res *app.DiffResult, err error) diffDoc {
	doc := diffDoc{
		Envelope: envelope("diff", err),
		From:     res.From,
		To:       res.To,
		Path:     res.Path,
		Files:    []diffFileDoc{},
	}
	for _, f := range res.Files {
		doc.Files = append(doc.Files, diffFileDoc{
			Path:       f.Path,
			Status:     f.Status,
			Insertions: f.Insertions,
			Deletions:  f.Deletions,
			Patch:      f.Patch,
			Truncated:  f.Truncated,
		})
	}
	return doc
}
