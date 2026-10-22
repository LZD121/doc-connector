---
phase: 01-contracts-identity-infrastructure
plan: 04
subsystem: persistence
tags: [go, yaml, atomic-write, flock, config, state]
provides:
  - Hidden YAML config with unknown-key preservation
  - Atomic JSON state/fetched/journal writes
  - Cross-process project lock (exit 3 on contention)
  - init identity stage wired to CLI
affects: [init, status, push, pull, restore]
tech-stack:
  added: [go.yaml.in/yaml/v3, github.com/gofrs/flock]
  patterns: [temp+fsync+rename, kernel-released lock]
key-files:
  created: [internal/config/config.go, internal/state/state.go, internal/state/atomic.go, internal/state/lock.go, internal/machine/machine.go]
  modified: [internal/cli/init.go]
key-decisions:
  - "Unknown config keys warn and round-trip instead of being rejected"
  - "State writes are always atomic and 0600; fetch timestamps are a separate cache"
duration: 22min
completed: 2026-10-22
status: complete
---

# Phase 01 Plan 04: Hidden Config, State and Lock Summary

**Persistence primitives that later phases cannot get wrong.**

## Accomplishments
- Config schema, validation (id shape, docs_dir locality, embedded credentials, limits) and 0600 atomic save.
- State/journal/fetched schemas with atomic writes and identity consistency checks.
- `flock` lock with 250 ms polling; contention returns exit 3 with holder pid.
- `init` resolves identity, takes the lock, reports it, then refuses bootstrap without writing config.

## Files Created/Modified
- `internal/config/config.go`, `internal/state/{state,atomic,lock}.go`, `internal/machine/machine.go`, tests.

## Decisions & Deviations
- Rename retries on Windows are not implemented in v1 (documented limitation).

## Next Phase Readiness
- Phase 2 writes config/exclude/store using these primitives.
