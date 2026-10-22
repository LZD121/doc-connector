---
phase: 02-guardrails-and-init
plan: 01
subsystem: isolation
tags: [go, git, exclude, idempotency]
provides:
  - Managed exclude block with idempotent in-place replacement
  - .gitignore never touched
key-files:
  created: [internal/project/exclude.go, internal/project/exclude_test.go]
key-decisions:
  - "Block format is three fixed lines; malformed blocks are refused, not clobbered"
duration: 12min
completed: 2026-10-22
status: complete
---

# Plan 02-01 Summary

**One managed block in `.git/info/exclude`, byte-preserving everything else.**

## Accomplishments
- No-trailing-newline inputs preserved; replacement limited to the marked span.
- Space escaping and atomic 0600/0644-preserving writes.
- Test asserts `.gitignore` bytes are unchanged.
