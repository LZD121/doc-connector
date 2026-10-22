# Roadmap: doc-connector

## Milestones

- ✅ **v1.0 doc-connector** — Phases 1–5 (shipped 2026-10-22)
- ✅ **v2.0 可观测、本地控制、分发与聚合** — Phases 6–10 (shipped 2026-10-23)

## Phases

<details>
<summary>✅ v2.0 可观测、本地控制、分发与聚合 (Phases 6–10) — SHIPPED 2026-10-23</summary>

**Delivered:** JSON output contract for `status`/`push`/`pull`, read-only `doctor`, full error-hint coverage, `.docignore`, multi-directory mapping with the v1.0 layout preserved, opt-in symlinks, a worktree-aware `diff`, cross-repository `status`/`sync --root` with project locks on the write path, Homebrew cask + Scoop manifests, shell completion for bash/zsh/fish, and a `tui` conflict-triage screen. 5 phases, 21 plans, 12 requirements.

Full roadmap: `.planning/milestones/v2.0-ROADMAP.md`
Requirements: `.planning/milestones/v2.0-REQUIREMENTS.md`
Audit: `.planning/milestones/v2.0-MILESTONE-AUDIT.md` (includes the winget/licence deferral and three debts)

</details>

<details>
<summary>✅ v1.0 doc-connector (Phases 1–5) — SHIPPED 2026-10-22</summary>

**Delivered:** 一个单静态二进制，把项目私有文档目录同步到个人私有 git 仓库的 `<project_id>/` 子树：三方 `status`、journal + 原子写的 `pull`、本地优先的冲突副本、lease 保护的 `push`、离线 `log`/`restore`、隔离护栏与三平台发布流水线。5 phases, 23 plans, 49 requirements.

Full roadmap: `.planning/milestones/v1.0-ROADMAP.md`
Requirements: `.planning/milestones/v1.0-REQUIREMENTS.md`
Audit: `.planning/milestones/v1.0-MILESTONE-AUDIT.md`

</details>

## Next Milestone

Not started. Run `/gsd-new-milestone` to scope the next cycle; candidate work is listed in
`.planning/milestones/v2.0-MILESTONE-AUDIT.md` under Tech Debt / Deferred.

---
*Roadmap updated: 2026-10-23 after v2.0 milestone close*
