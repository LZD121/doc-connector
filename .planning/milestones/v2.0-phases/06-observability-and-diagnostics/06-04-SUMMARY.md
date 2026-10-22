---
phase: 06-observability-and-diagnostics
plan: 04
subsystem: cli
tags: [go, doctor, exit-codes, help]
provides:
  - doc-connector doctor subcommand with --json
  - exit mapping 0 / 3 / 4 driven by findings
key-files:
  created: [internal/cli/doctor.go, internal/cli/doctor_test.go]
  modified: [internal/cli/cli.go, internal/cli/help.go]
key-decisions:
  - "doctor does not set ok=false for failed checks: the run succeeded, the repository is unhealthy; the exit code carries that"
duration: 22min
completed: 2026-10-22
status: complete
---

# Plan 06-04 Summary

**The diagnosis is reachable from the CLI and scriptable.**

## Accomplishments
- Human report: `[ok|warn|fail] <id>: <detail>` plus an indented `hint:` line and a `summary:` footer.
- Exit codes: `4` when the local mirror cannot yield a remote tip, `3` for any other failing check, `0` when only warnings.
- `doctor --json` emits `checks[]` and `summary{ok,warn,fail}`; the root help lists the command.
