package cli

const rootUsage = `doc-connector — sync a private docs directory to your own private git repository

Usage:
  doc-connector <command> [flags]

Commands:
  init      Bind this repository to a private remote and hide the docs directory
  status    Compare local docs, baseline and remote (read-only)
  push      Publish the local docs directory to <project_id>/ on the private remote
  pull      Apply remote changes to the local docs directory
  log       Show sync history from the local mirror (offline)
  restore   Write a historical version of a path back to the docs directory

Flags:
  -h, --help     Show this help
      --version  Show the version

Exit codes:
  0  success or nothing to do
  1  conflict or manual decision required
  2  usage, identity or environment error
  3  isolation (tracked docs, unsafe path) or lock failure
  4  network or authentication failure
  130 interrupted

Run "doc-connector <command> --help" for command flags.
`

const initUsage = `Usage:
  doc-connector init --remote <url> [flags]

Binds the current repository to a private git repository. Resolves the project
identity from the root commit, and (in later phases) writes the hidden exclude
block, creates the local bare mirror and records the hidden config.

Flags:
      --remote <url>        Private git remote URL (required)
      --project-id <id>     Explicit 12-hex project id (requires --force on mismatch)
      --branch <name>       Remote branch (default: detected, else main)
      --docs-dir <path>     Docs directory relative to the project root (default: private-docs)
      --machine <name>      Machine identity for conflict suffixes (default: hostname)
      --force               Accept an explicit project id that differs from the derived one
  -h, --help                Show this help
`

const statusUsage = `Usage:
  doc-connector status [flags]

Reports clean/ahead/behind/diverged plus per-file three-way state. Read-only.

Flags:
      --offline   Do not fetch the remote; report the cached remote tip
  -h, --help      Show this help
`

const pushUsage = `Usage:
  doc-connector push [flags]

Publishes the local docs directory as the <project_id>/ subtree of the private
remote. Refuses to overwrite any remote version the local side has not seen.

Flags:
      --dry-run            Print the plan without writing
      --resolve <path>     Accept the local version for an unresolved conflict (repeatable)
      --allow-large        Allow files above the 100 MiB hard limit
  -h, --help               Show this help
`

const pullUsage = `Usage:
  doc-connector pull [flags]

Applies remote additions, modifications and deletions to the docs directory.
Conflicts keep the local file and leave a <name>.theirs.<machine> copy.

Flags:
      --dry-run   Print the plan without writing
  -h, --help      Show this help
`

const logUsage = `Usage:
  doc-connector log [flags]

Shows sync history from the local mirror. Works offline.

Flags:
      --path <path>   Only commits touching this path (relative to the docs directory)
      --limit <n>     Maximum commits to show (default 20)
  -h, --help          Show this help
`

const restoreUsage = `Usage:
  doc-connector restore <commit> <path> [flags]

Writes a historical version of a path back into the docs directory. Refuses to
overwrite unsynced local changes unless --force is given. Never commits.

Flags:
      --from <commit>   Revision to read (default: remote tip)
      --force           Overwrite local changes and resolve the matching conflict
      --to <dir>        Write into another directory instead of the docs directory
  -h, --help            Show this help
`

var commandUsage = map[string]string{
	"init":    initUsage,
	"status":  statusUsage,
	"push":    pushUsage,
	"pull":    pullUsage,
	"log":     logUsage,
	"restore": restoreUsage,
}

var commandSummary = map[string]string{
	"init":    "Bind this repository to a private remote",
	"status":  "Compare local docs, baseline and remote",
	"push":    "Publish docs to the private remote",
	"pull":    "Apply remote changes locally",
	"log":     "Show sync history from the local mirror",
	"restore": "Restore a historical path",
}
