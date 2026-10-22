---
phase: 07-local-controls-and-history
plan: 04
subsystem: sync
tags: [go, multi-dir, pull, push, journal]
provides:
  - "docsEntry list with one fsx.Root, budget and mapping each"
  - "project-path mapping used by scan, push, pull, journal, restore and log"
  - "unmapped remote path guard (code 3) before any write"
key-files:
  created: [internal/cli/multidocs_test.go]
  modified: [internal/app/{context,status,push,pull,journal,restore,log}.go, internal/cli/output.go]
key-decisions:
  - "A root mapping (empty prefix) owns every project path by definition — that is what keeps the v1.0 layout working; the unmapped guard therefore only fires for all-prefixed configurations"
  - "Journal ops record project paths so recovery re-resolves after a config change"
duration: 11min
completed: 2026-10-22
status: complete
---

# Plan 07-04 Summary

**`<project_id>/` can hold several trees and every command agrees which file belongs where.**

## Accomplishments
- `ScanLocal` merges per-directory trees under project keys and runs the case-collision check across the merged set.
- `push` refuses a remote tree that holds paths no mapping owns (it would otherwise delete them), including on the race-retry path.
- `restore`/`log` accept `<prefix>/<rel>` or a bare `<rel>` when exactly one mapping could own it.
- Status JSON gained `project.docs_dirs`; `docs_dir` remains the first path.
