---
phase: 05-history-hardening-release
plan: 03
subsystem: hardening
tags: [go, unicode, nfc, casefold, windows]
provides:
  - NFC(lower) collision detection for local and remote trees
  - Windows reserved/length rules and mode normalization
  - Byte-exact CRLF/UTF-16 round-trip
key-files:
  created: [internal/fsx/casefold.go]
key-decisions:
  - "Collisions fail the whole operation before any write"
duration: 20min
completed: 2026-10-22
status: complete
---

# Plan 05-03 Summary

**The same tree must be expressible on Linux, macOS and Windows.**

## Accomplishments
- a.md/A.md and NFC/NFD duplicates are refused with every path listed.
- Windows mode differences with equal OIDs are normalized before classification.
- Round-trip test covers CRLF, NUL bytes and UTF-16.
