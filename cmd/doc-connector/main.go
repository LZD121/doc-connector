// Command doc-connector synchronizes a project's private documentation
// directory with a `<project_id>/` subtree of the user's own private git
// repository, without ever touching the team repository's history or index.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/LZD121/doc-connector/internal/cli"
)

// Stamped by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx, os.Args[1:], cli.Env{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Stdin:   os.Stdin,
		Version: version,
		Commit:  commit,
		Date:    date,
	}))
}
