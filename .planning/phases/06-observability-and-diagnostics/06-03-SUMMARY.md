---
phase: 06-observability-and-diagnostics
plan: 03
subsystem: diagnostics
tags: [go, doctor, lock, exclude]
provides:
  - app.Doctor read-only check engine with a fixed nine-check order
  - state.Probe read-only lock inspection
key-files:
  created: [internal/app/doctor.go, internal/app/doctor_test.go]
  modified: [internal/state/lock.go]
key-decisions:
  - "doctor never fetches: an empty mirror ref is a warning, not a network failure"
  - "lockInfo was replaced by the exported state.Holder so Acquire, lockedError and Probe share one shape"
duration: 38min
completed: 2026-10-22
status: complete
---

# Plan 06-03 Summary

**One command answers "why is it refusing?" without changing anything.**

## Accomplishments
- Checks run in a fixed order and never short-circuit except after repository discovery: `git-version`, `repository`, `exclude`, `untracked`, `ignored`, `config`, `lock`, `remote`, `artifacts`.
- Details name concrete paths (exclude file, config.yaml, state.json, leftover temp/journal/`.theirs.` files), never "somewhere in the config".
- `state.Probe` reads the lock file without taking the lock, so a held project is reportable instead of fatal.
- `TestDoctorReadOnly` asserts the check engine creates no directory, writes no file and leaves `git status --porcelain` empty.
