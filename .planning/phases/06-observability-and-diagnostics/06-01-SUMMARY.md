---
phase: 06-observability-and-diagnostics
plan: 01
subsystem: cli-output
tags: [go, json, exit-codes]
provides:
  - JSON envelope with schema_version/command/ok
  - closed-set string codes mapped from exit codes
  - one-document-per-run guard (wroteDoc marker + Run fallback)
key-files:
  created: [internal/cli/output.go]
key-decisions:
  - "The fallback document lives in Run and is suppressed by a wroteDoc marker, so partial results are never followed by a second document"
  - "--json is detected from raw argv so a failure before flag parsing still produces a document"
duration: 26min
completed: 2026-10-22
status: complete
---

# Plan 06-01 Summary

**Every run can be read by a machine, including the failing ones.**

## Accomplishments
- `Envelope` carries `schema_version:1`, `command`, `ok`, and an `error{code,message,hint}` object; payload structs embed it so the envelope always encodes first.
- `codeName` maps the closed exit-code set onto `conflict/usage/precondition/remote/interrupted`, with `internal` as the only escape.
- `jsonRequested` parses raw argv (`--json=false` is not a request), which is what lets `Run` emit a document for a usage error that never reached flag parsing.
- ANSI never reaches stdout in JSON mode; warnings keep using stderr.
