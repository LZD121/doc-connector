---
phase: 06-observability-and-diagnostics
plan: 05
subsystem: errors
tags: [go, hints, contract]
provides:
  - every app errf site carries a hint (0 gaps, enforced by inspection)
  - boundary test asserting hints appear in the user-visible error path
key-files:
  created: [internal/cli/hints_test.go]
  modified: [internal/app/context.go, internal/app/journal.go, internal/app/restore.go]
key-decisions:
  - "A hint states a verified state or a runnable command; speculation (maybe/possibly) is asserted against in the test"
duration: 18min
completed: 2026-10-22
status: complete
---

# Plan 06-05 Summary

**"What do I do now?" has an answer on every failure path.**

## Accomplishments
- Nine `errf` sites gained hints: docs-dir creation, four journal-recovery paths, unknown revision, missing docs dir, blob read failure, non-file restore.
- The audit found 27 `errf` call sites and 19 existing hints; the remaining 8 gaps (plus one found during the audit) are closed — a source scan now reports 0 sites without an attached hint.
- `TestEveryErrorCarriesHint` reads `error.hint` from the JSON document (same string as the human `hint:` line) for three reachable failures and rejects speculative wording.
