---
phase: 07-local-controls-and-history
plan: 03
subsystem: config
tags: [go, yaml, schema, exclude]
provides:
  - "config schema 2 with docs_dirs [{path,prefix}]"
  - "schema-1 files normalized to one empty-prefix mapping (v1.0 layout)"
  - "one exclude line and one guard pass per mapped directory"
key-files:
  created: []
  modified: [internal/config/config.go, internal/config/config_test.go, internal/cli/init.go, internal/project/exclude.go, internal/project/guard.go]
key-decisions:
  - "docs_dir stays as a mirror of docs_dirs[0].Path: readable YAML plus compatibility for any reader that still looks at it"
  - "Migration happens in memory on Load; the file is only rewritten by the next Save/init"
duration: 22min
completed: 2026-10-22
status: complete
---

# Plan 07-03 Summary

**A project can own several document trees without changing where v1.0 put files.**

## Accomplishments
- `validateDocsDirs` rejects duplicate or nested paths/prefixes and unsafe prefixes, each with a hint.
- `ExcludeBlockDirs` writes one anchored pattern per directory; `EnsureExcludeDirs` and `GuardAllDirs` cover every mapping.
- `init` re-run preserves the other mappings in both the config and the exclude block (`TestInitKeepsEveryMapping`).
