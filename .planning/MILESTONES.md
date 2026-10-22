# Project Milestones: doc-connector

## v2.0 可观测、本地控制、分发与聚合 (Shipped: 2026-10-23)

**Delivered:** JSON output for status/push/pull, a read-only `doctor`, full hint coverage, `.docignore`, multi-directory mapping, opt-in symlinks, a worktree-aware `diff`, cross-repository `status/sync --root`, push/pull project locks, Scoop/cask channels, shell completion, and a `tui` conflict-triage screen.

**Phases completed:** 5 phases (6-10), 21 plans

**Audit:** `.planning/milestones/v2.0-MILESTONE-AUDIT.md`

**Not shipped:** winget manifest (blocked on choosing a LICENSE — recorded as DIST-04)

**Key accomplishments:**
- Every run can be read by a machine, including the failing ones.
- The three sync commands answer in snake_case without changing a byte of their human output.
- One command answers "why is it refusing?" without changing anything.
- The diagnosis is reachable from the CLI and scriptable.
- "What do I do now?" has an answer on every failure path.
- Local files stay local without editing anything tracked.
- Symlinked notes can be synced, but only on purpose.
- A project can own several document trees without changing where v1.0 put files.
- `<project_id>/` can hold several trees and every command agrees which file belongs where.
- History is inspectable without touching the network or the docs directory.
- The workspace is a directory, not a registry.
- One screen answers which repository needs you.
- Two writers can no longer interleave on one project.
- One command drives the whole workspace without ever deciding a conflict for you.
- The cask channel is proven, not assumed.
- Scoop ships; winget waits for a licence, on purpose.
- Tab completion knows every command and every v2 flag.
- The workspace has a screen, and it refuses to render when nobody can see it.
- A conflict is two keystrokes from resolved, and the decision is still the human's.
- The screen cannot drift from the command line.

---

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
