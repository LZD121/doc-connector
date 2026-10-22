---
phase: 07-local-controls-and-history
plan: 05
subsystem: history
tags: [go, diff, mirror]
provides:
  - "diff <commit> [path] comparing two project subtrees"
  - "--json with per-file status, counts, patch and truncated flag"
key-files:
  created: [internal/app/diff.go, internal/app/diff_test.go, internal/cli/diff.go, internal/cli/diff_test.go]
  modified: [internal/cli/cli.go]
key-decisions:
  - "The store is a bare mirror with no worktree, so `git diff <base>:<pid>` (tree vs worktree) is impossible; the command diffs `<base>:<pid>` against the remote-tracking tip's `<pid>`, substituting the empty tree when a side lacks the prefix"
  - "Path filters are bare subtree paths, not <pid>/<path> (verified: the prefixed form matches nothing)"
duration: 7min
completed: 2026-10-22
status: complete
---

# Plan 07-05 Summary

**History is inspectable without touching the network or the docs directory.**

## Accomplishments
- Revision resolution mirrors `restore` (commit → `--from` → remote-tracking tip) and unknown revisions exit 2 with a hint.
- Per-file status A/M/D, insertion/deletion counts, binary markers, and a documented 64 KiB patch cap.
- `TestDiffCommand` also proves the command still works after the remote URL is pointed at a nonexistent path (no fetch).
