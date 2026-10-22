---
phase: 01-contracts-identity-infrastructure
plan: 03
subsystem: identity
tags: [go, git, project-id, shallow-clone]
provides:
  - Repo discovery with bare/linked-worktree rejection
  - project_id = lower(root_hex)[:12] with SHA-1/SHA-256 support
  - Shallow/unborn/multi-root rejection and explicit-id override
affects: [init, status, push, pull]
tech-stack:
  added: []
  patterns: [pure derivation function + minimal git probing]
key-files:
  created: [internal/project/project.go, internal/project/identity.go]
  modified: []
key-decisions:
  - "Multiple roots are never resolved by picking one: explicit --project-id only"
duration: 20min
completed: 2026-10-22
status: complete
---

# Phase 01 Plan 03: Project Identity Summary

**Same clone, same id, anywhere; ambiguous histories are refused.**

## Accomplishments
- `Discover` reports worktree/gitdir/common-dir/object-format/shallow and rejects bare (3) and linked worktrees (3).
- `DeriveID` follows the frozen rule and the 743c420a8e7b test vector.
- `Resolve` enforces explicit-id equality unless `--force`, and accepts an explicit id when derivation is impossible.

## Files Created/Modified
- `internal/project/{project,identity}.go`, `project_test.go`.

## Decisions & Deviations
- Discovery uses several `rev-parse` calls instead of one multi-argument call for clearer failure attribution.

## Next Phase Readiness
- `init` consumes `Resolve`; Phase 2 persists `id_source`.
