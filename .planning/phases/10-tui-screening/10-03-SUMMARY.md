---
phase: 10-tui-screening
plan: 03
subsystem: tui
tags: [go, testing, architecture]
provides:
  - "TUI/CLI equivalence tests driven by key scripts"
  - "architecture guard: tui.go has no gitstore/exec access"
key-files:
  created: [internal/cli/tui_test.go]
  modified: []
key-decisions:
  - "The equivalence test asserts observable repository state (clean status, file bytes, no .theirs artifact) rather than comparing commit hashes, because a resolve legitimately creates different commit metadata per run"
duration: 8min
completed: 2026-10-23
status: complete
---

# Plan 10-03 Summary

**The screen cannot drift from the command line.**

## Accomplishments
- `TestTuiKeepLocalMatchesCLI` ends with `status: clean`, local bytes intact and no conflict copy.
- `TestTuiTakeRemoteRestoresBlob` restores the remote blob verbatim.
- `TestTuiHasNoDirectGitAccess` fails the build if `tui.go` ever imports git plumbing directly.
- Verified on a real pseudo-terminal (`script -qec`) that the list renders, `j` moves and Enter opens the conflict screen.
