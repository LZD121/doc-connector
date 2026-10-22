# Requirements: doc-connector

**Defined:** 2026-10-22
**Core Value:** 让私有文档在多台机器间可验证地同步，同时保证主仓库的历史与工作区永远不含它们。

## v1 Requirements

### 初始化与身份

- [ ] **INIT-01**: 用户可在主仓库执行 `init --remote <url>` 完成关联；命令输出 project_id、远端分支、文档目录与下一步应执行的命令
- [x] **INIT-02**: 工具从根提交 OID 推导 `project_id = lower(root_hex)[:12]`；同一仓库的完整克隆在任意机器上得到相同 ID
- [x] **INIT-03**: 当仓库无根提交、有多个根提交、或为浅克隆且未提供 `--project-id` 时，init 拒绝执行（退出 2）并说明具体原因
- [ ] **INIT-04**: `init --project-id <12hex>` 可显式指定 ID 并记录 `id_source: explicit`；当它与可推导 ID 不一致时必须同时提供 `--force`
- [ ] **INIT-05**: init 把文档目录写入 `.git/info/exclude`（标记块，可重复执行），且从不修改 `.gitignore`
- [ ] **INIT-06**: init 通过 `ls-remote` 探测远端默认分支并正确处理空远端；认证或网络失败以退出 4 报告
- [ ] **INIT-07**: 当远端已存在 `<project_id>/` 时，init 采用该目录并提示用户先 `pull`，不覆盖本地任何文件
- [ ] **INIT-08**: 配置写入 `.git/doc-connector/config.yaml`（remote、branch、docs_dir、project_id、machine、id_source）；重复执行 init 是幂等的

### 状态与三方比较

- [ ] **SYNC-01**: `status` 逐文件显示 unchanged / local-only / remote-only / both-changed / deleted 变体，并汇总 clean、ahead、behind、diverged
- [ ] **SYNC-02**: 比较依据 git blob OID（内容哈希）；修改 mtime 或复制文件不产生假差异，同尺寸修改不会被漏报
- [ ] **SYNC-03**: 远端不可达时，`status` 仍报告本地与基线的状态，并明确标注“远端未检查”（退出 4）
- [ ] **SYNC-04**: status、push、pull 共用同一个三方分类实现；同一工作区状态下三者结论必须一致
- [ ] **SYNC-05**: `push --dry-run` 与 `pull --dry-run` 输出将执行的动作，不写工作区、不写状态、不写远端
- [ ] **SYNC-06**: 检测到 diverged（同一路径双方都改）时，push/pull 默认拒绝并退出 1，除非用户显式解决冲突

### 推送

- [ ] **PUSH-01**: `push` 把文档目录当前内容构造为 `<project_id>/` 子树并提交到私有仓库；不创建第二工作树，不触碰主仓库
- [ ] **PUSH-02**: push 保留远端根树中其他项目的子树与根级文件，只替换本项目的顶层条目
- [ ] **PUSH-03**: push 使用 `--force-with-lease`；非快进来自其他前缀时自动 fetch、重建并重试（最多 2 次），同前缀竞争则拒绝并提示 `pull`
- [ ] **PUSH-04**: 本地无变更时 push 为空操作（不产生空提交），退出 0
- [ ] **PUSH-05**: 只有推送成功后才把新 commit 记为基线；失败时保留 pending commit 供诊断，不推进基线
- [ ] **PUSH-06**: 每个提交携带 `Doc-Connector-Project`、`Doc-Connector-Machine`、`Doc-Connector-Version` trailers

### 拉取

- [ ] **PULL-01**: `pull` fetch 远端并把新增、修改、删除安全应用到文档目录；目录外的任何文件保持不变
- [ ] **PULL-02**: 所有写操作先写临时文件、fsync、再原子 rename；中途失败可依据 journal 恢复到一致状态
- [ ] **PULL-03**: 拉取前若本地存在与远端不同的同名文件（首次 pull 或基线缺失），保留本地并写 `<name>.theirs.<machine>`，退出 1
- [ ] **PULL-04**: pull 不删除“本地存在但基线中没有”的文件；删除仅对“基线存在、远端删除、本地未改”的路径生效
- [ ] **PULL-05**: pull 成功后推进基线；任何部分失败都不推进基线，并报告已应用与未应用的文件
- [ ] **PULL-06**: 远端树中的绝对路径、`..`、符号链接、Windows 保留名或超长路径被拒绝；整次操作失败且不写入任何文件

### 冲突解决

- [ ] **CONF-01**: modify/modify 冲突保留本地文件，并写 `<name>.theirs.<machine>`（重名依次 `.2`、`.3`），退出 1
- [ ] **CONF-02**: `.theirs.*` 产物被 status、push、pull 识别为冲突产物，不自动同步，也不计入普通变更
- [ ] **CONF-03**: `push --resolve <path>` 以本地内容覆盖远端冲突版本，并删除对应冲突产物
- [ ] **CONF-04**: `restore --force <path>` 以远端或指定历史版本覆盖本地，并删除对应冲突产物
- [ ] **CONF-05**: modify/delete 冲突保留本地决定（保留文件或保留删除），列出冲突并退出 1，不自动选择

### 历史与恢复

- [ ] **HIST-01**: `log` 离线读取本地镜像，显示时间、机器、提交摘要；`log --path <path>` 只显示影响该路径的提交
- [ ] **HIST-02**: `restore <commit> <path>` 把指定历史版本写回文档目录；该路径存在未同步改动时拒绝，`--force` 才覆盖
- [ ] **HIST-03**: restore 不自动提交；恢复结果作为一次普通本地变更，由后续 push 上传

### 安全护栏

- [ ] **GUARD-01**: 任何写操作前验证文档目录未被主仓库跟踪（`git ls-files -- <docs_dir>` 为空），否则退出 3
- [ ] **GUARD-02**: 验证文档目录的 ignore 来源正好是 `.git/info/exclude`（`check-ignore -v`）；`.gitignore` 的反选规则不得冒充
- [ ] **GUARD-03**: 工具不得修改主仓库 index、HEAD、refs、hooks 或已跟踪文件；集成测试对比每次操作前后这些集合
- [ ] **GUARD-04**: 所有文档读写限制在文档目录内（`os.Root` 语义），拒绝路径穿越与符号链接
- [ ] **GUARD-05**: 检测大小写碰撞与 Unicode NFC/NFD 归一化冲突；冲突时拒绝整次操作并列出冲突路径
- [x] **GUARD-06**: 跨进程锁保证同一 project/store 同时只有一个写操作；锁竞争等待有限时间后退出 3
- [ ] **GUARD-07**: 文件替换失败时保留临时文件并报错，绝不回退为原地截断写
- [ ] **GUARD-08**: 单文件超过 100MB 时拒绝同步（GitHub 硬限制）；超过 50MB 时警告但在 `--force` 下继续

### CLI 契约

- [x] **CLI-01**: `init`、`status`、`push`、`pull`、`log`、`restore` 六个子命令均提供 `--help`；根命令提供 `--version`
- [x] **CLI-02**: 退出码固定为 0 成功/无变更、1 冲突或需人工介入、2 用法/身份/环境错误、3 隔离或锁失败、4 网络或认证失败、130 中断
- [x] **CLI-03**: 正常结果写 stdout，诊断与错误写 stderr；任何命令都不发起交互式提问
- [x] **CLI-04**: 错误信息包含失败原因、退出码含义与相关路径；不输出未经验证的修复建议

### 跨平台与分发

- [ ] **PLAT-01**: 在 Linux、macOS、Windows 上构建并运行；不拼接 shell 字符串，全部通过 `exec.CommandContext` 参数切片调用 git
- [ ] **PLAT-02**: Windows 下正确处理大小写不敏感、保留名与 MAX_PATH；macOS 下 NFD 归一化不产生重复条目
- [ ] **PLAT-03**: GoReleaser 产出 linux/darwin/windows 的 amd64+arm64 归档与校验和；CI 在三平台矩阵运行测试

## v2 Requirements

### 可观测性

- **OBS-01**: `status --json`、`push --json`、`pull --json` 输出稳定 schema 供脚本消费
- **OBS-02**: `doctor` 诊断命令，检查 git 版本、排除规则来源、远端可达性、锁与状态一致性
- **OBS-03**: 错误信息附带可执行的下一步修复建议

### 本地控制

- **LOCL-01**: `.docignore` 本地忽略规则（不提交，支持 basename 与目录前缀）
- **LOCL-02**: 多文档目录映射（一个项目同步多个子树）
- **LOCL-03**: 符号链接支持（需显式开启，默认拒绝）
- **LOCL-04**: `diff <commit> <path>` 查看历史版本差异

### 分发与集成

- **DIST-01**: Homebrew tap / Scoop / winget 安装渠道
- **DIST-02**: shell completion（bash/zsh/fish）
- **DIST-03**: 自动创建私有远端仓库（`gh`/`bb` CLI 或 API token）

## Out of Scope

| Feature | Reason |
|---------|--------|
| 端到端加密 | 安全边界是私有仓库 ACL；密钥分发、轮换、丢失会成为新的失败模式。凭据已移出同步范围 |
| 凭据 / 密钥管理 | 改用密码管理器；同步工具不承担 secrets 生命周期 |
| watch / daemon / 定时同步 | 用户明确选择手动 CLI；守护进程会看到编辑器半写状态并在无人值守时制造冲突 |
| GUI / 移动端 / Web UI | 破坏单静态二进制与可脚本化目标，维护面成倍增加 |
| Git LFS / 大文件分块 | 目标是笔记与商务文档；LFS 指针使 log/restore 与冲突处理复杂化 |
| 云存储 API（S3/Drive/Dropbox） | 引入 SDK、凭据与最终一致性，且不提供 git 历史与三方比较 |
| 自动文本合并 | 散文合并会产生“看起来合理但语义错误”的文档，正是用户最担心的静默丢改动 |
| 自动创建私有仓库 | 需要 provider token、OAuth scope、2FA 与可见性决策；v1 手动建仓 |
| 复用主仓库隐藏 ref | 主仓库是团队仓库，不允许写入任何私人对象 |
| 跨项目共享文档目录 | 所有权与并发语义模糊；每个项目固定一个 `<project_id>/` |
| 在主仓库 commit / push | 违反核心价值；工具只读主仓库，唯一写入是 `.git/info/exclude` |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| INIT-01 | Phase 2 | Pending |
| INIT-02 | Phase 1 | Complete |
| INIT-03 | Phase 1 | Complete |
| INIT-04 | Phase 1 | In progress (CLI persistence in Phase 2) |
| INIT-05 | Phase 2 | Pending |
| INIT-06 | Phase 2 | Pending |
| INIT-07 | Phase 2 | Pending |
| INIT-08 | Phase 2 | Pending |
| SYNC-01 | Phase 3 | Pending |
| SYNC-02 | Phase 3 | Pending |
| SYNC-03 | Phase 3 | Pending |
| SYNC-04 | Phase 3 | Pending |
| SYNC-05 | Phase 4 | Pending |
| SYNC-06 | Phase 3 | Pending |
| PUSH-01 | Phase 4 | Pending |
| PUSH-02 | Phase 4 | Pending |
| PUSH-03 | Phase 4 | Pending |
| PUSH-04 | Phase 4 | Pending |
| PUSH-05 | Phase 4 | Pending |
| PUSH-06 | Phase 4 | Pending |
| PULL-01 | Phase 3 | Pending |
| PULL-02 | Phase 3 | Pending |
| PULL-03 | Phase 4 | Pending |
| PULL-04 | Phase 3 | Pending |
| PULL-05 | Phase 3 | Pending |
| PULL-06 | Phase 3 | Pending |
| CONF-01 | Phase 4 | Pending |
| CONF-02 | Phase 4 | Pending |
| CONF-03 | Phase 4 | Pending |
| CONF-04 | Phase 5 | Pending |
| CONF-05 | Phase 4 | Pending |
| HIST-01 | Phase 5 | Pending |
| HIST-02 | Phase 5 | Pending |
| HIST-03 | Phase 5 | Pending |
| GUARD-01 | Phase 2 | Pending |
| GUARD-02 | Phase 2 | Pending |
| GUARD-03 | Phase 2 | Pending |
| GUARD-04 | Phase 2 | Pending |
| GUARD-05 | Phase 5 | Pending |
| GUARD-06 | Phase 1 | Complete |
| GUARD-07 | Phase 3 | Pending |
| GUARD-08 | Phase 5 | Pending |
| CLI-01 | Phase 1 | Complete |
| CLI-02 | Phase 1 | Complete |
| CLI-03 | Phase 1 | Complete |
| CLI-04 | Phase 1 | Complete |
| PLAT-01 | Phase 1 | Partial (cross-build ok; runtime matrix in Phase 5) |
| PLAT-02 | Phase 5 | Pending |
| PLAT-03 | Phase 5 | Pending |

**Coverage:**
- v1 requirements: 49 total
- Mapped to phases: 49
- Unmapped: 0 ✅

**Per-phase distribution:**

| Phase | Requirements | Count |
|-------|--------------|-------|
| Phase 1 — 契约、身份与基础设施 | CLI-01..04, INIT-02, INIT-03, INIT-04, GUARD-06, PLAT-01 | 9 |
| Phase 2 — 隔离护栏与 init | INIT-01, INIT-05, INIT-06, INIT-07, INIT-08, GUARD-01..04 | 9 |
| Phase 3 — 状态与干净拉取 | SYNC-01..04, SYNC-06, PULL-01, PULL-02, PULL-04, PULL-05, PULL-06, GUARD-07 | 11 |
| Phase 4 — 冲突与推送 | SYNC-05, PULL-03, CONF-01, CONF-02, CONF-03, CONF-05, PUSH-01..06 | 12 |
| Phase 5 — 历史、硬化与发布 | HIST-01..03, CONF-04, GUARD-05, GUARD-08, PLAT-02, PLAT-03 | 8 |

---
*Requirements defined: 2026-10-22*
*Last updated: 2026-10-22 after roadmap creation (all 49 v1 requirements mapped)*
