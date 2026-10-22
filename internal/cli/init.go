package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/machine"
	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/state"
)

// lockWait bounds how long a second writer waits before exiting 3.
var lockWait = 10 * time.Second

// cmdInit resolves and validates the project identity.
//
// Phase 1 deliberately stops before touching the filesystem: writing a config
// without the exclude block would let users believe their docs are hidden while
// `git status` still shows them. The remote bootstrap, exclude block and bare
// mirror land in Phase 2; until then init refuses with exit 2 after reporting
// the identity it resolved.
func cmdInit(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remote := fs.String("remote", "", "private remote URL")
	projectID := fs.String("project-id", "", "explicit 12-hex project id")
	branch := fs.String("branch", "", "remote branch")
	docsDir := fs.String("docs-dir", "private-docs", "docs directory")
	machineFlag := fs.String("machine", "", "machine identity")
	force := fs.Bool("force", false, "accept an explicit id that differs from the derived one")

	helped, err := parseFlags(fs, args, e.Stdout, initUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}
	if strings.TrimSpace(*remote) == "" {
		return Errorf(CodeUsage, "init: --remote is required").
			WithHint("create an empty private repository and pass its URL, for example git@github.com:me/private-docs.git")
	}
	if *docsDir == "" {
		return Errorf(CodeUsage, "init: --docs-dir must not be empty")
	}
	r := gitstore.New(e.Cwd)
	if err := gitstore.EnsureMinimum(ctx, r); err != nil {
		return Wrap(err, CodeUsage)
	}
	if *branch != "" {
		if _, err := r.Run(ctx, "check-ref-format", "--branch", *branch); err != nil {
			return Errorf(CodeUsage, "init: invalid branch name %q", *branch).
				WithHint("git check-ref-format rejected it; use a name like \"main\" or \"docs/sync\"")
		}
	}

	repo, err := project.Discover(ctx, r, e.Cwd)
	if err != nil {
		return Wrap(err, CodeUsage)
	}

	id, err := project.Resolve(ctx, r, repo, *projectID, *force)
	if err != nil {
		return Wrap(err, CodeUsage)
	}

	name := machine.Sanitize(*machineFlag)
	if strings.TrimSpace(*machineFlag) == "" {
		name = machine.Hostname()
	}

	// Take the same lock every write path will take, so concurrent runs fail
	// predictably even while bootstrap is unfinished.
	lockCtx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	lock, err := state.Acquire(lockCtx, repo.GitDir, "init", name)
	if err != nil {
		return Wrap(err, CodeGuard)
	}
	defer lock.Release()

	fmt.Fprintf(e.Stdout, "project: %s (%s)\n", id.ID, id.Source)
	if id.RootCommit != "" {
		fmt.Fprintf(e.Stdout, "root commit: %s\n", id.RootCommit)
	}
	fmt.Fprintf(e.Stdout, "object format: %s\n", id.ObjectFormat)
	fmt.Fprintf(e.Stdout, "docs dir: %s\n", *docsDir)
	fmt.Fprintf(e.Stdout, "machine: %s\n", name)

	return Errorf(CodeUsage, "init: remote bootstrap is not implemented yet").
		WithHint("this build stops after identity resolution; no files were written")
}
