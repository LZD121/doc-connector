---
phase: 09-distribution-and-completion
plan: 02
subsystem: release
tags: [goreleaser, scoop, winget, license]
provides:
  - "Scoop manifest generation gated on SCOOP_BUCKET_GITHUB_TOKEN"
  - "winget deferred as DIST-04 with the blocking reason recorded"
key-files:
  created: []
  modified: [.goreleaser.yaml, .planning/REQUIREMENTS.md]
key-decisions:
  - "No LICENSE file exists, and a winget manifest requires a license field; choosing one is the owner's legal decision, so the tool does not invent it"
duration: 5min
completed: 2026-10-23
status: complete
---

# Plan 09-02 Summary

**Scoop ships; winget waits for a licence, on purpose.**

## Accomplishments
- `scoops:` writes `dist/scoop/bucket/doc-connector.json` in a snapshot and uploads only when the bucket token exists.
- DIST-01 was rewritten to what is actually delivered; winget moved to DIST-04 with the exact restart condition (add a LICENSE, then add the `winget:` section).
