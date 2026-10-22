---
phase: 08-cross-repo-aggregation
plan: 03
subsystem: locking
tags: [go, flock, push, pull]
provides:
  - "push/pull take the exclusive project lock (10s bounded wait) and always release"
key-files:
  created: [internal/app/lock_guard_test.go]
  modified: [internal/app/push.go, internal/app/pull.go]
key-decisions:
  - "Dry-run also takes the lock: it reads the baseline and the remote tip"
  - "The push retry path does not re-acquire (the lock is held for the whole command)"
duration: 2min
completed: 2026-10-23
status: complete
---

# Plan 08-03 Summary

**Two writers can no longer interleave on one project.**

## Accomplishments
- Closed a v1.0 gap: only `init` used the lock, so `push`/`pull` could race.
- Locked push/pull exit 3 naming the holder, with no commit, no journal and no docs change.
- `TestProjectLockReleasedOnAllPaths` covers success, dry-run and an early conflict error.
