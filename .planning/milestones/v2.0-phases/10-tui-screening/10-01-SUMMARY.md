---
phase: 10-tui-screening
plan: 01
subsystem: tui
tags: [go, tui, term]
provides:
  - "tui subcommand with TTY gate and alternate-screen lifecycle"
  - "injectable tuiApp (reader/writer) so tests need no pseudo-terminal"
key-files:
  created: [internal/cli/tui.go]
  modified: [internal/cli/cli.go, internal/cli/help.go]
key-decisions:
  - "golang.org/x/term is the only new dependency (IsTerminal + MakeRaw); no TUI framework"
  - "EOF on stdin is treated as quit, so a closed pipe cannot hang the screen"
duration: 14min
completed: 2026-10-23
status: complete
---

# Plan 10-01 Summary

**The workspace has a screen, and it refuses to render when nobody can see it.**

## Accomplishments
- Non-TTY runs exit 2 with a hint pointing at `status --root`; nothing half-drawn reaches stdout.
- Summary screen lists every repository with project id, state and verdict, driven by `app.WorkspaceStatus`.
