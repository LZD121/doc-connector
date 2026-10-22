---
phase: 03-status-and-pull
plan: 02
subsystem: trees
tags: [go, ls-tree, cat-file, baseline]
provides:
  - NUL-safe ls-tree parser with symlink rejection
  - Derive-vs-cache baseline loading
  - cat-file --batch streaming into staged files
key-files:
  created: [internal/trees/trees.go, internal/gitstore/blobs.go]
key-decisions:
  - "The synced commit is authoritative; baseline_tree is only a cache"
duration: 20min
completed: 2026-10-22
status: complete
---

# Plan 03-02 Summary

**Tree reads without a worktree; blobs stream, never load whole.**

## Accomplishments
- Missing prefix yields an empty tree; symlink/blob-type violations exit 3.
- baseline cache mismatch warns and falls back to the commit.
- WriteBlobs verifies OID/size and handles binary content byte-exactly.
