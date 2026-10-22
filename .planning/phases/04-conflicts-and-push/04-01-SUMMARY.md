---
phase: 04-conflicts-and-push
plan: 01
subsystem: conflicts
tags: [go, conflict-artifacts, unresolved]
provides:
  - <path>.theirs.<machine> naming with .2/.3 dedupe and long-path fallback
  - Conflict materialization during pull without second artifacts
  - Baseline held while unresolved
key-files:
  created: [internal/conflict/name.go]
  modified: [internal/app/pull.go]
key-decisions:
  - "Existing unresolved paths never get another artifact copy"
duration: 28min
completed: 2026-10-22
status: complete
---

# Plan 04-01 Summary

**Conflicts keep the local file and leave exactly one remote copy behind.**

## Accomplishments
- All four conflict shapes: add/add and both-modified write copies; delete/modify writes in the original parent; modify/delete only records.
- Non-conflicting remote changes still apply; baseline stays until resolution.
- Dedupe test proves a second pull does not stack `.2`.
