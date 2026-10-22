---
phase: 03-status-and-pull
plan: 01
subsystem: scan
tags: [go, ignore, hash-object, symlink]
provides:
  - Fixed junk filter (no .gitignore parsing)
  - Content-addressed local scan with mode and size limits
key-files:
  created: [internal/ignore/filter.go, internal/scan/scan.go, internal/gitstore/hash.go]
key-decisions:
  - "OID equality is the only change signal; mtime/size are never compared"
duration: 22min
completed: 2026-10-22
status: complete
---

# Plan 03-01 Summary

**touch is invisible, same-size edits are not; symlinks and junk never enter the tree.**

## Accomplishments
- `hash-object --no-filters --stdin-paths` batching with sorted paths.
- Symlink skip + warning; `100755` preserved; size ceiling with actionable error.
- `ValidateSyncPath` adds Windows reserved names and the length budget.
