---
phase: 02-guardrails-and-init
plan: 04
subsystem: init
tags: [go, config, state, idempotency, second-machine]
provides:
  - Config/state persistence as the final step of init
  - Byte-identical re-init and docs_dir reconfiguration
  - Second-machine clone deriving the same project id
key-files:
  modified: [internal/cli/init.go]
  created: [internal/cli/cli_test.go]
key-decisions:
  - "Config is written only after guard, exclude and remote checks pass"
  - "An existing project id is never repointed; a different id exits 2"
duration: 25min
completed: 2026-10-22
status: complete
---

# Plan 02-04 Summary

**init is idempotent and safe to re-run; a second machine converges on the same id.**

## Accomplishments
- First init: empty remote → base none, next push; existing prefix → base = tip, next pull.
- Re-init: config and state byte-identical, one exclude block, output identical.
- Snapshot assertion proves index/HEAD/refs/hooks/tracked files are unchanged.
