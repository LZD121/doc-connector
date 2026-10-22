---
phase: 06-observability-and-diagnostics
plan: 02
subsystem: cli-output
tags: [go, json, status, push, pull]
provides:
  - status/push/pull --json payloads
  - full-length OIDs in JSON, 12-char short form kept in human output
  - StatusResult fields the document needs (project, remote, base, age)
key-files:
  created: [internal/cli/json_test.go]
  modified: [internal/cli/status.go, internal/cli/push.go, internal/cli/pull.go, internal/app/status.go, internal/app/context.go]
key-decisions:
  - "JSON DTOs live in cli; app structs stay free of wire tags"
  - "status files[] lists only changed paths (status != \\\"\\\" and != \\\"=\\\")"
  - "FetchedAgeSeconds was split out of FetchedAge so the human and JSON renderers can never disagree about the cache age"
duration: 31min
completed: 2026-10-22
status: complete
---

# Plan 06-02 Summary

**The three sync commands answer in snake_case without changing a byte of their human output.**

## Accomplishments
- `status --json` reports `project{id,docs_dir}`, `remote{url,branch,tip,checked,fetched_at,fetched_age_seconds,fetch_error}`, `base{commit,tree}`, `state`, `counts{ahead,behind,conflicts}`, and `files[{path,status,local_oid,base_oid,remote_oid}]`.
- `push --json` reports counts, commit/tree/parent and resolved paths; `pull --json` reports writes/deletes, `applied[]`, `conflicted[]` and the remote tip.
- A failed fetch still emits the payload with `remote.fetch_error` set and exits 4.
- `TestHumanOutputUnchangedWithoutJSON` pins the human renderer.
