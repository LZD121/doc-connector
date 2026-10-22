---
phase: 04-conflicts-and-push
plan: 03
subsystem: push
tags: [go, push, clobber, retry, baseline]
provides:
  - End-to-end push with clobber gate, no-op detection and trailers
  - Rebuild-on-fresh-tip retry loop with local-change abort
  - Baseline advancement only after success
key-files:
  created: [internal/app/push.go, internal/cli/push.go]
key-decisions:
  - "A remote deletion of an unchanged local file is a clobber"
  - "Refused pushes keep pending and state untouched"
duration: 32min
completed: 2026-10-22
status: complete
---

# Plan 04-03 Summary

**Push publishes only what it has seen, and only after the remote accepts it.**

## Accomplishments
- Two projects coexist under one remote root; other entries untouched.
- Same content is a no-op (exit 0, no commit, no state write).
- Cross-machine clobber refuses with a pull hint and leaves the baseline put.
