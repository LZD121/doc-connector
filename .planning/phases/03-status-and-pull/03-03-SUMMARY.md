---
phase: 03-status-and-pull
plan: 03
subsystem: classify
tags: [go, three-way, pure-function]
provides:
  - 15-row Classify shared by status/pull/push
  - Summary direction and Clobber predicate
key-files:
  created: [internal/trees/classify.go]
key-decisions:
  - "Remote-deleted paths are clobbers too (union-based check)"
duration: 18min
completed: 2026-10-22
status: complete
---

# Plan 03-03 Summary

**One table, three commands, no chance to disagree.**

## Accomplishments
- Every row asserted, including mode changes and all four conflict shapes.
- Clobber covers remote-add/modify/delete of unseen versions; resolved paths exempt.
