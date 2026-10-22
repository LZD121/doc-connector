---
phase: 02-guardrails-and-init
plan: 03
subsystem: init
tags: [go, ls-remote, bare-mirror, adoption]
provides:
  - Remote branch detection with empty-remote handling
  - Bare mirror creation/fetch with glob refspec
  - <project_id>/ prefix adoption
key-files:
  created: [internal/gitstore/store.go, internal/gitstore/remote.go, internal/gitstore/store_test.go]
key-decisions:
  - "All ls-remote/fetch failures map to exit 4"
duration: 18min
completed: 2026-10-22
status: complete
---

# Plan 02-03 Summary

**init bootstraps a bare mirror without ever checking out a worktree.**

## Accomplishments
- Empty remote detected via `--exit-code` exit 2 with empty stderr.
- `init --bare -b <branch>` only when the mirror is absent.
- Prefix presence determines the adoption branch (pull guidance).
