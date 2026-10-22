---
phase: 07-local-controls-and-history
plan: 01
subsystem: ignore
tags: [go, docignore, filter]
provides:
  - ".docignore per docs root: basename and directory-prefix rules"
  - "unsupported syntax reported once per file, matched literally"
key-files:
  created: []
  modified: [internal/ignore/filter.go, internal/app/context.go, internal/app/pull.go]
key-decisions:
  - "No glob engine: ! and ** are matched literally with one warning per file, per FEATURES.md's anti-feature stance"
  - ".docignore is in the built-in junk set, so it can never be synced from any docs root"
duration: 7min
completed: 2026-10-22
status: complete
---

# Plan 07-01 Summary

**Local files stay local without editing anything tracked.**

## Accomplishments
- `ignore.NewWithDocsIgnore(extra, docsDirs)` merges the built-in junk set with each root's `.docignore`; `New` keeps its old semantics.
- `pull` drops ignored paths from classification and from the apply plan, so an ignored file is never written, deleted, or turned into a `.theirs.` copy.
- `TestDocIgnoreEndToEnd` proves a basename rule and a directory rule both vanish from status and from the pushed tree, and survive a pull untouched.
