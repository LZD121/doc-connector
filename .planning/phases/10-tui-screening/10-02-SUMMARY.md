---
phase: 10-tui-screening
plan: 02
subsystem: tui
tags: [go, tui, conflicts]
provides:
  - "conflict triage: l → push --resolve, t → restore --force"
  - "completion table includes tui"
key-files:
  created: []
  modified: [internal/cli/tui.go, internal/cli/completion.go]
key-decisions:
  - "The TUI never decides content: it calls the same app functions the CLI does, then re-reads the conflict list"
duration: 9min
completed: 2026-10-23
status: complete
---

# Plan 10-02 Summary

**A conflict is two keystrokes from resolved, and the decision is still the human's.**

## Accomplishments
- Both sides are reachable: keep local or take remote, with feedback on the status line.
- Conflicts are re-read after every action, so the screen cannot show a stale list.
