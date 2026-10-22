---
phase: 08-cross-repo-aggregation
plan: 02
subsystem: workspace
tags: [go, status, json, exit-codes]
provides:
  - "status --root aggregate with verdicts current/ahead/behind/blocked/failed/locked"
  - "status --root --json"
  - "worst-of-N exit code"
key-files:
  created: [internal/cli/workspace.go, internal/cli/workspace_test.go]
  modified: [internal/cli/status.go, internal/cli/help.go]
key-decisions:
  - "Per-repo failures are data: the walk continues and the exit code carries the worst rank"
  - "--offline/--follow-symlinks are rejected with --root rather than silently ignored"
duration: 4min
completed: 2026-10-23
status: complete
---

# Plan 08-02 Summary

**One screen answers which repository needs you.**

## Accomplishments
- Serial per-repo `app.Status`; one bad repo cannot hide the rest.
- Exit code is the maximum per-repo code over rank 0<1<2<3<4 (130 excluded).
