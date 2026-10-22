---
phase: 09-distribution-and-completion
plan: 01
subsystem: release
tags: [goreleaser, homebrew]
provides:
  - "verified cask generation and token-gated upload"
key-files:
  created: []
  modified: [.goreleaser.yaml]
key-decisions:
  - "No upload is attempted in this phase: the tap token is the owner's secret"
duration: 3min
completed: 2026-10-23
status: complete
---

# Plan 09-01 Summary

**The cask channel is proven, not assumed.**

## Accomplishments
- `goreleaser check` validates the config; a snapshot run writes `dist/homebrew/Casks/doc-connector.rb` alongside six platform archives and `checksums.txt`.
- `skip_upload` keeps the release green without `HOMEBREW_TAP_GITHUB_TOKEN`.
