---
phase: 07-local-controls-and-history
plan: 02
subsystem: scan
tags: [go, symlinks, scan]
provides:
  - "--follow-symlinks opt-in on status/push/pull"
  - "target bytes stored at the link path with a regular mode"
key-files:
  created: []
  modified: [internal/scan/scan.go, internal/app/context.go, internal/app/push.go, internal/app/pull.go, internal/cli/{status,push,pull}.go]
key-decisions:
  - "Default stays 'refuse the whole operation' — silently skipping is how documents disappear"
  - "Hashing uses the resolved target path while the tree key stays the link path, so the remote never sees a 120000 entry"
duration: 7min
completed: 2026-10-22
status: complete
---

# Plan 07-02 Summary

**Symlinked notes can be synced, but only on purpose.**

## Accomplishments
- Absolute targets, `..` escapes, dangling links and directory targets are refused with code 3 and the full offender list.
- The chain is validated with `EvalSymlinks` against the root, not just the first hop.
- Size gates apply to the resolved target, so a link cannot bypass the 100 MiB limit.
