---
phase: 01-contracts-identity-infrastructure
plan: 01
subsystem: cli
tags: [go, cli, exit-codes, flag]
provides:
  - Six-subcommand CLI surface with stable exit codes
  - Root --version/--help and per-command --help
  - Stable "not implemented yet" stubs for five commands
affects: [all later phases]
tech-stack:
  added: []
  patterns: [stdlib flag with ContinueOnError, coded errors]
key-files:
  created: [cmd/doc-connector/main.go, internal/cli/cli.go, internal/cli/exit.go, internal/cli/help.go, internal/cli/init.go]
  modified: [go.mod]
key-decisions:
  - "Exit codes are the closed CLI-02 set {0,1,2,3,4,130}; the research documents' code 5 is deliberately not used"
duration: 18min
completed: 2026-10-22
status: complete
---

# Phase 01 Plan 01: CLI Contract Summary

**Six documented commands, stdout/stderr separation, usage errors and stable stubs.**

## Accomplishments
- `cli.Run` dispatches init/status/push/pull/log/restore and maps errors to exit codes.
- `--version` and every `--help` work without a repository; unimplemented commands exit 2.
- `main` only wires signal cancellation (130), version stamping and exit.

## Files Created/Modified
- `cmd/doc-connector/main.go` — assembly only.
- `internal/cli/{cli,exit,help,init}.go` — dispatch, exit contract, usage, init identity stage.
- `internal/cli/cli_test.go` — surface contract tests.

## Decisions & Deviations
- Root-level `help`/`version` subcommand spellings are accepted in addition to flags.

## Next Phase Readiness
- Phase 2 replaces the stubs and completes `init`.
