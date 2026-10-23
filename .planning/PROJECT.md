# doc-connector

## What This Is

一个 Go 单二进制 CLI，把项目里的私有文档目录映射到**个人私有 git 仓库**中的一个哈希目录，并在多台机器之间 `push` / `pull`。文档始终以普通文件形式存在于主仓库工作区的子目录中，但被 `.git/info/exclude` 排除，因此主仓库的历史、分支列表和 `git status` 永远不会出现它们。

它面向“主仓库是团队仓库、不能放私人文件”的开发者：代码走团队仓库，私人笔记与商务文档走自己的私有仓库，两条轨道互不干扰。

## Core Value

让私有文档在多台机器间**可验证地**同步，同时保证主仓库的历史与工作区永远不含它们。任何取舍都优先保证“不丢改动”和“不误入主仓库”。

## Next Milestone

Not started. v2.0 shipped 2026-10-23 (phases 6-10, 12 requirements). Deferred themes
are recorded in `.planning/milestones/v2.0-MILESTONE-AUDIT.md` (winget licence, signing,
field validation) and in `REQUIREMENTS.md` Future entries of the archive; run
`/gsd-new-milestone` to scope the next cycle.

## Requirements

### Validated

v1.0 shipped 2026-10-22 — all 49 requirements complete. See `.planning/milestones/v1.0-REQUIREMENTS.md`.

- [x] `init --remote <url>` binds the project to a private docs repository with hidden config and a managed exclude block
- [x] Deterministic 12-hex project id from the root commit, with explicit override and ambiguity refusal
- [x] `private-docs/` is hidden via `.git/info/exclude`; `.gitignore` is never touched
- [x] `push` publishes `<project_id>/` with lease protection, retry, trailers and no-op detection
- [x] `pull` applies remote adds/mods/deletes atomically under a journal, never deleting local-only files
- [x] `status` reports clean/ahead/behind/diverged and per-file variants from blob OIDs
- [x] Conflicts keep local bytes and leave exactly one `.theirs.<machine>` copy; `push --resolve` closes them
- [x] `log` works offline; `restore` recovers history without auto-committing
- [x] Junk files, symlinks, case/NFC collisions and size limits are refused with actionable messages
- [x] Three-platform CI and GoReleaser configuration for six static targets

### Active

None — v2.0 is complete. See `.planning/milestones/v2.0-REQUIREMENTS.md` for what shipped.

### Out of Scope

- **凭据/密钥同步** — 改用密码管理器；本工具不承担 secrets 管理
- **端到端加密** — 安全边界是私有仓库的访问控制；加密会显著增加密钥管理成本
- **watch / daemon / 定时同步** — 用户选择手动 CLI，避免后台进程的不确定性
- **自动创建远端仓库** — v1 由用户在 GitHub/Bitbucket 手动建空私有仓库
- **复用主仓库的隐藏 ref** — 主仓库通常是不可控的团队仓库，不得写入任何对象
- **GUI 客户端 / 移动端 / Web UI** — 纯 CLI；若未来出现非 CLI 用户，形态是 `doc-connector serve` 本地 Web UI，而非原生壳
- **全局 workspace 配置文件** — `--root` 参数即 workspace；不引入 `~/.config` 状态，保持“全部状态在仓库内”
- **并发推送同一私有远端** — 共享私有仓连续推进分支会撞 lease 重试上限；批量必须串行且每仓重新 fetch
- **文件级自动文本合并** — 冲突只生成副本，合并由人决定
- **Git LFS / 大文件分块** — 目标内容是笔记与商务文档，不处理 GB 级数据
- **跨项目共享单个文档目录** — 每个项目独立前缀，避免所有权模糊

## Context

- **现状**：多台电脑之间靠 U 盘/手动拷贝传递文档。
- **最不能接受的三个问题**：不知道文件是否最新；冲突导致内容丢失；`git add -A` 误提交私密文档。
- **主仓库属性**：多为团队或公司仓库，用户没有改写历史或添加跟踪文件的权限，因此任何方案都不能在主仓库产生提交或修改被跟踪的忽略文件。
- **私有仓库属性**：位于 GitHub 或 Bitbucket，私有可见；本质上是任意可通过系统 git 访问的 remote。
- **文档类型**：项目笔记、会议纪要、调研草稿、商务资料、个人记录。单文件通常小于 100MB，总规模数十到数百个文件。
- **平台**：Linux/WSL（当前开发环境）、macOS、Windows。

## Constraints

- **语言/分发**：Go 单二进制，无运行时依赖；`GOOS` 交叉编译覆盖 linux/darwin/windows。
- **Git 版本**：依赖底层 plumbing，要求系统 `git >= 2.30`。
- **认证**：不实现自己的认证，shell out 到 `git`，复用 SSH agent、credential helper、代理配置。
- **历史完整性**：浅克隆无法推导真实根提交，必须报错并要求 `--project-id` 显式指定，绝不猜测。
- **主仓库不可污染**：不得写入 `.gitignore`、不得修改 index、不得创建提交。唯一的例外是 `.git/info/exclude`（本地、未跟踪）。
- **配置隐身**：配置存于 `.git/doc-connector/config.yaml`，随克隆丢失；新机器必须重新 `init`。这是用户明确选择的代价。
- **安全边界**：项目哈希是身份校验而非认证；任何能读到主仓库的人都能算出它，真正的访问控制由私有仓库 ACL 提供。

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| 独立私有 docs 仓库，不复用主仓库 | 主仓库是团队仓库，不能写入任何私人对象 | — Validated in v1.0 |
| 项目标识 = 根提交 OID 前 12 位十六进制字符（不再二次哈希） | 跨克隆确定性且不可变；`.git` 路径与本地 UUID 跨机器不一致，无法用于匹配。SHA-1 与 SHA-256 仓库同样适用；多根提交或浅克隆时拒绝并要求 `--project-id` | — Validated in v1.0 |
| 多项目共用一个私有仓库，按 `<project_id>/` 前缀分目录 | 单处管理，避免仓库数量膨胀；哈希前缀天然避免重名 | — Validated in v1.0 |
| 文档目录 `private-docs/` + `.git/info/exclude` | 主仓库 `git status` 保持干净，且不修改团队共享的 `.gitignore` | — Validated in v1.0 |
| 配置存 `.git/doc-connector/config.yaml` | 用户要求完全不进仓库；代价是每台新机器重新 init | — Validated in v1.0 |
| 上次同步 commit 作为三方基线 | 能区分“本地改了”“远端改了”“双方都改了”，避免静默覆盖 | — Validated in v1.0 |
| 冲突保留本地 + `.theirs.<host>` 副本，退出码非零 | 用户最不能接受丢改动；二进制文件无法自动合并 | — Validated in v1.0 |
| 不加密，凭据移出同步范围 | 私有仓库 ACL 为边界；避免 age/GPG 密钥在多机间的管理负担 | — Validated in v1.0 |
| Go + shell out 到 git | 复用系统凭据、SSH、代理配置；单文件分发，跨三平台 | — Validated in v1.0 |
| 裸镜像 `.git/doc-connector/store.git` + plumbing | 文档只有一份，避免第二工作树与工作区漂移；`log`/`restore` 直接读镜像 | — Validated in v1.0 |
| 手动 CLI（init/push/pull/status/log/restore） | 用户明确不要后台 watch/hook；同步时机完全可控 | — Validated in v1.0 |
| 内置垃圾文件忽略列表 | 避免 `.DS_Store` 等系统文件污染私有仓库 | — Validated in v1.0 |
| workspace 只由 `--root <dir>` 参数确定（深度≤2 发现已初始化仓库），不引入全局配置文件 | 保持“工具的全部状态都在仓库内”，避免陈旧条目与仓库搬迁两类故障 | — Validated in v2.0 |
| `sync --root` 逐仓串行、每仓重新 fetch；diverged 或有冲突→`blocked` 跳过；退出码取最坏值 | 共享私有远端连续推进分支会撞 lease 重试上限；批量绝不自动 resolve | — Validated in v2.0 |
| TUI 只做跨仓汇总屏 + 冲突分诊，全部复用 `internal/app`，TUI 层零 git 调用 | 两份 UI 各自调 git 会让 TUI 判定与 `status` 分叉，违反“一个分类器是正确性核心” | — Validated in v2.0 |
| GUI 客户端不做 | 破坏单静态二进制；macOS 未签名导致下载即 Gatekeeper 拦截；需求本质是减少 cd，键盘更合适 | — Held in v2.0 |
| DIST-03（自动建远端）暂缓到 v2 之后 | 需 provider token、组织策略与可见性决策，`research/FEATURES.md` 列为 anti-feature | — Held in v2.0 |
| 许可证 = MIT（2026 起，版权人 LZD121） | winget 清单与 GitHub 分发都需要明确的 license 字段；MIT 与本工具“单二进制、可自由复用”的定位一致 | — Decided 2026-10-23 |
| winget 清单随 `WINGET_GITHUB_TOKEN` 存在而自动开 PR，缺失则只落 `dist/` | 与 cask/Scoop 同一规则：没有凭据的渠道不能让发布变红 | — Decided 2026-10-23 |

## Current State

**v2.0 shipped (2026-10-23).** 151 tests across 13 packages, `go vet` clean, `golangci-lint` 0 issues, `goreleaser check` valid, snapshot produces six archives plus cask and Scoop manifests. The CLI now has eleven subcommands (`init`, `status`, `push`, `pull`, `log`, `restore`, `doctor`, `diff`, `sync`, `tui`, `completion`). Still not exercised against a real provider private repository or a second physical machine — that field validation remains the highest-value next step.

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition:**
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone:**
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-10-23 after v2.0 milestone close*
