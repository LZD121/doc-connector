---
phase: 08-cross-repo-aggregation
plan: 01
subsystem: workspace
tags: [go, discovery, workspace]
provides:
  - "Discover(root) → RepoRef list at depth <= 2, skipped count"
key-files:
  created: [internal/app/workspace.go, internal/app/workspace_test.go]
key-decisions:
  - "A repo without config counts toward skipped instead of vanishing"
  - "Config presence alone decides initialization, so worktree .git files work"
duration: 4min
completed: 2026-10-23
status: complete
---

# Plan 08-01 Summary

**The workspace is a directory, not a registry.**

## Accomplishments
- Depth <= 2, no symlink traversal, dot-dirs plus node_modules/vendor skipped, results sorted.
- Depth-3 repositories are provably excluded and a missing root exits 2 with a hint.
