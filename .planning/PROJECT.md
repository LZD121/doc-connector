# doc-connector

## What This Is

一个 Go 单二进制 CLI，把项目里的私有文档目录映射到**个人私有 git 仓库**中的一个哈希目录，并在多台机器之间 `push` / `pull`。文档始终以普通文件形式存在于主仓库工作区的子目录中，但被 `.git/info/exclude` 排除，因此主仓库的历史、分支列表和 `git status` 永远不会出现它们。

它面向“主仓库是团队仓库、不能放私人文件”的开发者：代码走团队仓库，私人笔记与商务文档走自己的私有仓库，两条轨道互不干扰。

## Core Value

让私有文档在多台机器间**可验证地**同步，同时保证主仓库的历史与工作区永远不含它们。任何取舍都优先保证“不丢改动”和“不误入主仓库”。

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] `init --remote <url>` 将当前主仓库关联到私有 docs 仓库，并写入隐身配置
- [ ] 项目标识由主仓库根提交 SHA 确定性推导，跨克隆一致，可显式覆盖
- [ ] 文档目录默认 `private-docs/`，自动写入 `.git/info/exclude`，不改动共享的 `.gitignore`
- [ ] `push` 把本地文档提交到私有仓库的 `<project_id>/` 目录
- [ ] `pull` 把远端变更写回本地，支持新增/修改/删除
- [ ] `status` 明确显示 clean / ahead / behind / diverged 以及逐文件差异
- [ ] 冲突时保留本地文件并生成 `.theirs.<host>` 副本，绝不静默覆盖
- [ ] `log` 查看同步历史，`restore` 从历史提交恢复单个文件
- [ ] 内置垃圾文件忽略列表（`.DS_Store`、`Thumbs.db`、`desktop.ini` 等）
- [ ] 拒绝在文档未被正确排除、或 `project_id` 不匹配时执行任何写操作

### Out of Scope

- **凭据/密钥同步** — 改用密码管理器；本工具不承担 secrets 管理
- **端到端加密** — 安全边界是私有仓库的访问控制；加密会显著增加密钥管理成本
- **watch / daemon / 定时同步** — 用户选择手动 CLI，避免后台进程的不确定性
- **自动创建远端仓库** — v1 由用户在 GitHub/Bitbucket 手动建空私有仓库
- **复用主仓库的隐藏 ref** — 主仓库通常是不可控的团队仓库，不得写入任何对象
- **移动端 / Web UI** — 纯 CLI
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
| 独立私有 docs 仓库，不复用主仓库 | 主仓库是团队仓库，不能写入任何私人对象 | — Pending |
| 项目标识 = 根提交 OID 前 12 位十六进制字符（不再二次哈希） | 跨克隆确定性且不可变；`.git` 路径与本地 UUID 跨机器不一致，无法用于匹配。SHA-1 与 SHA-256 仓库同样适用；多根提交或浅克隆时拒绝并要求 `--project-id` | — Pending |
| 多项目共用一个私有仓库，按 `<project_id>/` 前缀分目录 | 单处管理，避免仓库数量膨胀；哈希前缀天然避免重名 | — Pending |
| 文档目录 `private-docs/` + `.git/info/exclude` | 主仓库 `git status` 保持干净，且不修改团队共享的 `.gitignore` | — Pending |
| 配置存 `.git/doc-connector/config.yaml` | 用户要求完全不进仓库；代价是每台新机器重新 init | — Pending |
| 上次同步 commit 作为三方基线 | 能区分“本地改了”“远端改了”“双方都改了”，避免静默覆盖 | — Pending |
| 冲突保留本地 + `.theirs.<host>` 副本，退出码非零 | 用户最不能接受丢改动；二进制文件无法自动合并 | — Pending |
| 不加密，凭据移出同步范围 | 私有仓库 ACL 为边界；避免 age/GPG 密钥在多机间的管理负担 | — Pending |
| Go + shell out 到 git | 复用系统凭据、SSH、代理配置；单文件分发，跨三平台 | — Pending |
| 裸镜像 `.git/doc-connector/store.git` + plumbing | 文档只有一份，避免第二工作树与工作区漂移；`log`/`restore` 直接读镜像 | — Pending |
| 手动 CLI（init/push/pull/status/log/restore） | 用户明确不要后台 watch/hook；同步时机完全可控 | — Pending |
| 内置垃圾文件忽略列表 | 避免 `.DS_Store` 等系统文件污染私有仓库 | — Pending |

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
*Last updated: 2026-10-22 after initialization*
