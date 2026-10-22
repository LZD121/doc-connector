---
phase: 04-conflicts-and-push
plan: 05
subsystem: resolve
tags: [go, resolve, artifacts]
provides:
  - push --resolve <path> (repeatable) with strict membership check
  - Artifact removal and unresolved cleanup after success
key-files:
  modified: [internal/app/push.go, internal/cli/push.go]
key-decisions:
  - "Unknown --resolve paths exit 2; artifacts survive a failed push"
duration: 12min
completed: 2026-10-22
status: complete
---

# Plan 04-05 Summary

**Resolution is explicit, scoped and only committed once the push lands.**

## Accomplishments
- Resolved paths bypass clobber; remote is overwritten with local bytes.
- Artifact and unresolved entry disappear only after a successful push.
