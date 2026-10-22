---
phase: 05-history-hardening-release
plan: 05
subsystem: release
tags: [ci, goreleaser, homebrew]
provides:
  - 3 OS × 2 Go CI matrix with vet/test/race/lint/tidy
  - GoReleaser v2 config for six targets, checksums and a Homebrew cask
key-files:
  created: [.github/workflows/ci.yml, .goreleaser.yaml, Makefile, .gitignore]
key-decisions:
  - "goreleaser v2 only; homebrew_casks, never deprecated brews"
duration: 15min
completed: 2026-10-22
status: complete
---

# Plan 05-05 Summary

**Tagging v* is the whole release action.**

## Accomplishments
- Static builds (CGO off), trimpath, version/commit/date stamped.
- Windows archives are zip; others tar.gz; checksums.txt.
- Cask upload is skipped for snapshots.
