---
phase: 05-history-hardening-release
plan: 02
subsystem: restore
tags: [go, restore, conflicts]
provides:
  - restore <path> / <commit> <path> / --from / --to
  - Refusal on unsynced local changes, --force override
  - Conflict artifact removal and unresolved clearing
key-files:
  created: [internal/app/restore.go, internal/cli/restore.go]
key-decisions:
  - "Restore never commits or advances the baseline"
  - "Flags may follow positionals (reordered before flag parsing)"
duration: 24min
completed: 2026-10-22
status: complete
---

# Plan 05-02 Summary

**Recover any historical byte without ever silently losing a local edit.**

## Accomplishments
- Deleted files are recovered from their last touching commit.
- --force removes the matching .theirs copy and clears unresolved when the restored OID matches the remote version.
- --to writes outside the docs tree without touching state.
