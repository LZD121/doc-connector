---
phase: 02-guardrails-and-init
plan: 02
subsystem: isolation
tags: [go, check-ignore, os.Root, snapshot]
provides:
  - Tracked-docs and ignore-source guards (exit 3)
  - Repository snapshot for GUARD-03 assertions
  - os.Root path confinement and validation
key-files:
  created: [internal/project/guard.go, internal/fsx/root.go, internal/fsx/validate.go]
key-decisions:
  - "check-ignore is parsed from -z --stdin NUL fields (never colon splitting)"
duration: 20min
completed: 2026-10-22
status: complete
---

# Plan 02-02 Summary

**Guardrails run before any write; os.Root confines every docs path.**

## Accomplishments
- `GuardAll` order: untracked, then ignore-source equality.
- Snapshot hashes index/HEAD/refs/hooks/tracked; test proves exclude+store+writes leave them intact.
- Path rules reject traversal, backslashes, control chars, Windows-invalid characters and `.git` components.
