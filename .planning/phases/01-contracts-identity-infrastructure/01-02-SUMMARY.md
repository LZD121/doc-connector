---
phase: 01-contracts-identity-infrastructure
plan: 02
subsystem: git-exec
tags: [go, exec, git, environment]
provides:
  - Single git execution seam with argv slices and scrubbed env
  - git >= 2.30 precondition gate
  - Classified git errors (remote / race / other)
affects: [project, config, all sync phases]
tech-stack:
  added: []
  patterns: [exec.CommandContext, env allowlist-by-removal]
key-files:
  created: [internal/gitstore/exec.go, internal/gitstore/version.go, internal/gitstore/classify.go]
  modified: []
key-decisions:
  - "GIT_TERMINAL_PROMPT is always 0: CLI-03 forbids interactive prompts"
duration: 15min
completed: 2026-10-22
status: complete
---

# Phase 01 Plan 02: Git Execution Seam Summary

**One place runs git; everything else consumes classified results.**

## Accomplishments
- Argument slices only, explicit Dir, 8 inherited GIT_* variables stripped.
- `git version` parsed for Apple/Windows suffixes; < 2.30 rejects before any action.
- Remote/authentication vs race vs other classification lives in `Classify`.

## Files Created/Modified
- `internal/gitstore/{exec,version,classify}.go` and `exec_test.go`.

## Decisions & Deviations
- `Run` returns stderr only on failure; streaming callers use `Cmd` (phase 3).

## Next Phase Readiness
- `fetch/push/refs` verbs land in later phases on top of `Cmd`/`Run`.
