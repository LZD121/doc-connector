---
phase: 05-history-hardening-release
plan: 04
subsystem: limits
tags: [go, size-gate]
provides:
  - 50 MiB warning tier requiring --force
  - 100 MiB hard limit with --allow-large override
key-files:
  modified: [internal/scan/scan.go, internal/app/pull.go, internal/app/push.go]
key-decisions:
  - "status never refuses on size; it is read-only"
duration: 12min
completed: 2026-10-22
status: complete
---

# Plan 05-04 Summary

**Large files are a decision, not an accident.**

## Accomplishments
- Local scan and remote blob precheck share the same two tiers.
- Tests scale limits down to 10/20 bytes for determinism.
