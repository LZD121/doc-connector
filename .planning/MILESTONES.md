# Project Milestones: doc-connector

## v1.0 doc-connector (Shipped: 2026-10-22)

**Delivered:** A single static Go binary that keeps a project's private documentation in `<project_id>/` of the user's own private git repository while the team repository stays byte-for-byte untouched.

**Phases completed:** 1-5 (23 plans total)

**Key accomplishments:**
- CLI contract with a closed exit-code set, argv-only git execution and a hidden YAML config
- Deterministic project identity with shallow/multi-root refusal and explicit override
- Managed `.git/info/exclude` plus guardrails that refuse tracked or foreign-ignored docs
- Content-OID three-way classifier shared by status/pull/push
- Journaled, atomic pull with conflict copies and forward recovery
- Lease-guarded push with subtree splicing, retry, trailers and dry-run
- Offline log, history restore, case/NFC and size hardening
- Three-platform CI matrix and GoReleaser v2 packaging

**Stats:**
- 131 files changed
- ~9,100 Go lines across 61 files (including tests)
- 5 phases, 23 plans, 86 test functions
- 1 day from roadmap to ship

**Git range:** `docs: freeze project identity rule` → `feat(05): offline history, restore, hardening and release pipeline`

**What's next:** v2 themes: structured JSON output, `doctor`, `.docignore`, multi-directory mapping, shell completion.

---
