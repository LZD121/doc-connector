# Requirements: doc-connector

**Defined:** 2026-10-22
**Milestone:** v2.0 可观测、本地控制、分发与聚合
**Core Value:** 让私有文档在多台机器间可验证地同步，同时保证主仓库的历史与工作区永远不含它们。

Sources: `.planning/milestones/v1.0-REQUIREMENTS.md` (v2 Requirements section: OBS/LOCL/DIST) plus WORK requirements agreed on 2026-10-22 for cross-repository aggregation. Research baseline: `.planning/research/` (v1) — no new research run; the only unstudied surface is the TUI toolkit choice, deferred to plan-phase 10.

## v2.0 Requirements

Committed scope. Every requirement maps to exactly one phase in `ROADMAP.md`.

### 可观测性

- [ ] **OBS-01**: 用户可用 `status --json`、`push --json`、`pull --json` 获得稳定 schema 的结果，字段含 `schema_version`、`command`、`ok`；失败时同样输出单个 JSON 对象（`error.code`/`error.message`/`error.hint`）并以非零码退出，stdout 不含 ANSI
- [ ] **OBS-02**: 用户可用 `doctor` 一次性诊断：git 版本、`.git/info/exclude` 中 managed 块的来源与生效性、文档路径是否被跟踪、远端可达性、当前锁持有者、base 与远端 tip 的一致性、遗留临时/冲突产物
- [ ] **OBS-03**: 每条错误路径都带 `hint`，且 hint 只陈述工具已实际验证过的状态或可直接执行的命令；`--json` 下同一 hint 出现在 `error.hint` 字段（当前 32 处 `withHint` 为既有基础，本项把它升级为全覆盖契约 + 测试断言）

### 本地控制

- [ ] **LOCL-01**: 用户可在文档根放置 `.docignore`（不提交、不推送、不进 status），支持 basename 与目录前缀两种模式，叠加在内置垃圾列表之上，并作用于 status/push/pull 三处
- [ ] **LOCL-02**: 用户可为一个项目配置多个文档目录（config 从单 `docs_dir` 升级为目录列表），每目录独立映射到 `<project_id>/<prefix>`；旧单目录 config 自动迁移，`--force` 之外不改写
- [ ] **LOCL-03**: 用户可显式开启符号链接同步（默认仍拒绝），链接目标必须解析在文档根内；关闭时行为与 v1.0 一致
- [ ] **LOCL-04**: 用户可用 `diff <commit> [path]` 查看本项目前缀下某历史版本的差异，输出可直读也可 `--json`，只读镜像不触网

### 跨仓库聚合

- [ ] **WORK-01**: 用户可用 `status --root <dir>` 发现根目录下深度 ≤2 的已初始化仓库（凭 `<git>/doc-connector/config.yaml`），逐仓汇总 `project_id`、`docs_dir`、state、ahead/behind、冲突数；无 config 的仓库静默跳过并单独计数
- [ ] **WORK-02**: 用户可用 `sync --root <dir>` 批量同步：逐仓串行，clean→跳过、behind→pull、ahead→push、diverged 或存在冲突→标记 `blocked` 并跳过（绝不自动 resolve）、单仓失败→记录后继续；退出码取最坏值；`--dry-run` 零写入
- [ ] **WORK-03**: 用户可用 `tui` 看到跨仓库汇总屏并完成冲突分诊（选仓库→看冲突→选 local/remote→调 `push --resolve`/`restore --force`）；TUI 层零 git 调用，全部复用 `internal/app`

### 分发与集成

- [ ] **DIST-01**: tag 发布时自动产出 Homebrew cask（`LZD121/homebrew-tap`）、Scoop 清单与 winget 清单；cask 上传在 `HOMEBREW_TAP_GITHUB_TOKEN` 存在时生效，缺失时只生成到 `dist/` 且发布保持绿色
- [ ] **DIST-02**: 用户可用 `doc-connector completion bash|zsh|fish` 生成补全脚本，覆盖六个子命令与新增标志

## Future Requirements

Acknowledged, not in this roadmap.

### 分发

- **DIST-03**: 自动创建私有远端仓库（`gh`/`bb` CLI 或 API token）— 暂缓：需要 provider token、组织策略与可见性决策；`research/FEATURES.md` 将其列为 anti-feature，`PROJECT.md` Out of Scope 记为“由用户手动建空私有仓库”。重启条件：出现需要一条命令完成引导的真实场景，且 `gh auth status` 可被无副作用探测

### 运维

- **OPS-01**: `store gc` / 镜像修剪策略
- **OPS-02**: 次级远端复制（备份远端）
- **OPS-03**: `verify` / 镜像与远端 fsck

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| 端到端加密 | 安全边界是私有仓库 ACL；密钥分发、轮换、丢失会成为新的失败模式 |
| 凭据 / 密钥管理 | 改用密码管理器；同步工具不承担 secrets 生命周期 |
| watch / daemon / 定时同步 | 用户明确选择手动 CLI；守护进程会看到编辑器半写状态并在无人值守时制造冲突 |
| GUI 客户端 / 移动端 / Web UI | 破坏单静态二进制与可脚本化目标，维护面成倍增加；macOS 二进制未签名，浏览器下载会被 Gatekeeper 拦。若未来出现非 CLI 用户，形态是 `doc-connector serve` 本地 Web UI，而非原生壳 |
| 全局 workspace 配置文件（如 `~/.config/doc-connector/workspaces.yaml`） | 打破“工具的全部状态都在仓库内”的性质，新增陈旧条目与仓库搬迁两类故障；workspace 只由 `--root` 参数确定 |
| 并发推送同一私有远端 | 共享私有仓连续推进分支会撞 lease 重试上限（`internal/app/push.go` 的 retries exhausted）；批量必须串行且每仓重新 fetch |
| 自动解决冲突 | 散文与二进制无法安全合并；冲突只生成副本，决定由人做 |
| 文件级自动文本合并 | 同上；静默 auto-merge 正是用户最担心的丢改动 |
| Git LFS / 大文件分块 | 目标是笔记与商务文档；LFS 指针使 log/restore 与冲突处理复杂化 |
| 跨项目共享单个文档目录 | 每个项目独立前缀，避免所有权模糊 |
| 复用主仓库的隐藏 ref | 主仓库通常是不可控的团队仓库，不得写入任何对象 |
| 在主仓库 commit / push | 违反核心价值；工具只读主仓库，唯一写入是 `.git/info/exclude` |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| OBS-01 | Phase 6 | Pending |
| OBS-02 | Phase 6 | Pending |
| OBS-03 | Phase 6 | Pending |
| LOCL-01 | Phase 7 | Pending |
| LOCL-02 | Phase 7 | Pending |
| LOCL-03 | Phase 7 | Pending |
| LOCL-04 | Phase 7 | Pending |
| WORK-01 | Phase 8 | Pending |
| WORK-02 | Phase 8 | Pending |
| DIST-01 | Phase 9 | Pending |
| DIST-02 | Phase 9 | Pending |
| WORK-03 | Phase 10 | Pending |

**Coverage:**
- v2.0 requirements: 12 total
- Mapped to phases: 12
- Unmapped: 0 ✓

---
*Requirements defined: 2026-10-22*
*Last updated: 2026-10-22 after milestone v2.0 initialization*
