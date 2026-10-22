# Roadmap: doc-connector

## Overview

v1.0 把一个「裸仓库 + 一行 exclude」的手工脚本变成可信任的单二进制工具：先把 CLI 契约、确定性身份与隐藏配置钉死，再用 `init` 与隔离护栏保证主仓库永远干净；随后以共享的三方分类器为核心交付只读的 `status` 与安全的 `pull`，再在有 `clobber` 判据与冲突闸门之后才开放 `push`；最后补齐离线 `log`/`restore`、跨平台与体积边界以及三平台发布流水线。每个阶段结束时，用户都能跑一条真实命令并观察到可验证的结果——不是脚手架，而是能用的能力。

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

- [ ] **Phase 1: 契约、身份与基础设施** - 六命令骨架与退出码契约、git 执行缝隙、确定性 project_id、隐藏配置/状态与单实例锁
- [ ] **Phase 2: 隔离护栏与 init** - `init` 端到端可用；隐身 exclude 与写前护栏保证文档永不进入主仓库
- [ ] **Phase 3: 状态与干净拉取** - 三方分类器 + `status` 报告；`pull` 以 journal 与原子写安全应用增删改
- [ ] **Phase 4: 冲突与推送** - 本地优先的冲突副本与显式解决；租约推送、子树拼接、重试与基线推进
- [ ] **Phase 5: 历史、硬化与发布** - 离线 `log`/`restore`、路径与体积边界、三平台 CI 与 GoReleaser 发布

## Phase Details

### Phase 1: 契约、身份与基础设施
**Goal**: 交付可构建的单二进制骨架：六个子命令、稳定的退出码与输出契约先于业务逻辑存在，同时提供确定性的 project_id 推导、隐藏配置与原子状态存储、跨进程锁——后续每个阶段都通过这套契约说话。
**Mode:** mvp
**Depends on**: Nothing (first phase)
**Requirements**: CLI-01, CLI-02, CLI-03, CLI-04, INIT-02, INIT-03, INIT-04, GUARD-06, PLAT-01
**Success Criteria** (what must be TRUE):
  1. 用户运行 `doc-connector --version` 或任一子命令的 `--help`，无需仓库即可获得用法说明并以退出码 0 结束；尚未实现的子命令以退出码 2 明确说明未实现。
  2. 同一主仓库的完整克隆在任意路径、任意机器上推导出相同的 12 位十六进制 project_id；浅克隆、无根提交或多根提交时命令以退出码 2 拒绝并列出具体原因。
  3. `init --project-id <12hex>` 与可推导 ID 不一致且未给 `--force` 时以退出码 2 拒绝；给了 `--force` 时配置记录 `id_source: explicit`。
  4. 所有 git 调用以参数切片执行（无 shell 字符串拼接），系统 git < 2.30 时任何子命令都在动作前失败并说明原因；结果写 stdout、诊断写 stderr，且全程不发起交互式提问。
  5. 同一 project/store 上两个写操作并发启动时，后者在有限等待后以退出码 3 结束，前者正常完成。
**Plans**: 4 plans

Plans:
- [ ] 01-01: `cmd/doc-connector` 与 `internal/cli`：六个子命令注册、`--help`/`--version`、flag 校验、stdout/stderr 分离、退出码常量与错误映射（0/1/2/3/4/130）
- [ ] 01-02: `internal/gitstore` Runner：唯一 git 执行缝隙——参数切片、env 清洗（GIT_DIR/GIT_WORK_TREE/非交互）、`GitError{Args,Code,Stderr}` 分类、上下文超时、`git --version >= 2.30` 前置门
- [ ] 01-03: `internal/project`：仓库定位（工作树/公共 git 目录、linked worktree 与子模块拒绝）、根提交组合解析、`DeriveID = lower(hex)[:12]` 纯函数与测试向量（SHA-1 与 SHA-256）、浅克隆/多根/无根拒绝、`--project-id` + `--force` 路径
- [ ] 01-04: `internal/config` 与 `internal/state`：YAML `KnownFields` 解析、JSON 原子写（temp+fsync+rename）、`state/journal/fetched` 骨架、机器身份（清洗 hostname，可覆盖）、`gofrs/flock` 单实例锁与有限等待

### Phase 2: 隔离护栏与 init
**Goal**: 让 `init` 端到端可用——绑定私有远端、把文档目录写入隐身 exclude、建立裸镜像；并在任何写操作前拒绝被跟踪或未被本工具规则排除的文档目录，使「绝不误入主仓库」成为可验收的保证。
**Mode:** mvp
**Depends on**: Phase 1
**Requirements**: INIT-01, INIT-05, INIT-06, INIT-07, INIT-08, GUARD-01, GUARD-02, GUARD-03, GUARD-04
**Success Criteria** (what must be TRUE):
  1. 用户在主仓库执行 `init --remote <url>` 后得到 project_id、远端分支、文档目录与下一步命令，配置落在 `.git/doc-connector/config.yaml`（remote、branch、docs_dir、project_id、machine、id_source），重复执行不改变结果。
  2. 私有文档目录不出现在 `git status` 中，团队仓库的 `.gitignore` 字节未变，`.git/info/exclude` 里只有一段受管标记块且重复 init 不重复追加。
  3. 文档目录已被主仓库跟踪、或其 ignore 来源不是 `.git/info/exclude`（例如 `.gitignore` 的 `!private-docs/` 反选）时，所有命令以退出码 3 拒绝，并打印来源文件与模式。
  4. 空远端可被引导；远端已存在 `<project_id>/` 时 init 采用该前缀并提示先 `pull`，不修改本地任何文档文件；认证或网络失败以退出码 4 报告。
  5. 任一命令执行前后，主仓库的 index 哈希、HEAD、refs、hooks 与已跟踪文件集合完全一致。
**Plans**: 4 plans

Plans:
- [ ] 02-01: docs 目录解析与 exclude 块写入器：受管标记、幂等、无尾换行文件字节安全、永不触碰 `.gitignore`
- [ ] 02-02: 隔离护栏流水线：`ls-files -z -- <docs>` 为空校验、`check-ignore -v -n -z --no-index` 来源必须为解析后的 `.git/info/exclude`、index/HEAD/refs/hooks 前后快照对比、`os.Root` 限定文档读写
- [ ] 02-03: `init` 主流程：remote 校验（含内嵌凭据拒绝）、`ls-remote` 探测默认分支与空远端、`store.git` 裸镜像引导或 fetch、已存在 `<project_id>/` 的收养语义
- [ ] 02-04: 配置落盘与幂等重入；`--branch`/`--docs-dir`/`--force` 选项；第二台机器流程（clone → init 得到同一 project_id）

### Phase 3: 状态与干净拉取
**Goal**: 交付只读的三方比较核心与第一次入境同步：`status` 按内容 OID 报告 clean/ahead/behind/diverged 及逐文件差异，`pull` 在护栏、journal 与原子写保护下安全应用新增、修改与删除。
**Mode:** mvp
**Depends on**: Phase 2
**Requirements**: SYNC-01, SYNC-02, SYNC-03, SYNC-04, SYNC-06, PULL-01, PULL-02, PULL-04, PULL-05, PULL-06, GUARD-07
**Success Criteria** (what must be TRUE):
  1. `status` 给出 clean/ahead/behind/diverged 汇总与逐文件 unchanged/local-only/remote-only/both-changed/deleted 变体，并对双方都改的路径明确点名。
  2. `touch` 文件或复制同内容文件后 `status` 仍报 unchanged，同尺寸内容的修改被报为变更——差异完全来自 blob OID 而非 mtime 或大小。
  3. 远端不可达时 `status` 仍报告本地与基线状态、明确标注「远端未检查」，并以退出码 4 结束；`status` 与 `pull` 的预检共用同一个分类实现，对同一状态结论一致。
  4. `pull` 只改动文档目录：远端新增/修改/删除按计划应用，删除仅对「基线存在、远端删除、本地未改」的路径生效，本地存在但基线中没有的文件永不删除；目录外文件字节不变；diverged 时默认拒绝并以退出码 1 结束。
  5. 远端树中的 `..`、绝对路径、符号链接、Windows 保留名或超长路径使整次 pull 失败且不写入任何文件；中途 kill 后重跑依据 journal 收敛到一致状态，替换失败时保留临时文件而绝不原地截断写。
**Plans**: 5 plans

Plans:
- [ ] 03-01: `internal/ignore` 内置垃圾表（`.DS_Store`、`Thumbs.db`、`desktop.ini` 等）与保留后缀；`internal/fsx` 扫描与 `hash-object -w --no-filters` 内容 OID、可执行位、符号链接拒绝、大小统计
- [ ] 03-02: `internal/trees` 只读侧与 `gitstore` 的 fetch、`cat-file --batch` 流式读取、基线加载（校验 `state.synced_commit:<pid>` 与其缓存树一致，不符以提交为准）
- [ ] 03-03: `Classify(B,L,R)` 十五行判定表与 `Plan` 构造（纯函数 + 表驱动测试），并抽出 push 复用的 `clobber` 判据
- [ ] 03-04: `status` 命令：汇总与逐文件变体、`--offline`、fetch 失败退回本地缓存状态并退出 4、冲突记录展示
- [ ] 03-05: `pull` 应用层：journal 状态机与崩溃恢复、逐文件 temp+fsync+rename、删除与空目录清理规则、路径校验管线整批中止

### Phase 4: 冲突与推送
**Goal**: 完成写路径的另一半：本地优先的冲突产物与显式解决，以及把本地文档作为 `<project_id>/` 子树推送到共享私有仓库的租约推送、竞争重试与基线推进。
**Mode:** mvp
**Depends on**: Phase 3
**Requirements**: SYNC-05, PULL-03, CONF-01, CONF-02, CONF-03, CONF-05, PUSH-01, PUSH-02, PUSH-03, PUSH-04, PUSH-05, PUSH-06
**Success Criteria** (what must be TRUE):
  1. 双方修改同一路径时保留本地文件并生成 `<name>.theirs.<machine>`（重名依次 `.2`/`.3`），列出冲突并以退出码 1 结束；首次 pull（无基线）遇到同名不同内容文件同样保留本地并留下副本，绝不静默覆盖。
  2. `.theirs.*` 产物不出现在远端、不被 `status`/`push` 当作普通变更；未解决冲突时 `push` 以退出码 1 拒绝，`push --resolve <path>` 后恢复正常并删除对应产物。
  3. 两个不同项目推送到同一个私有仓库后，各自 `<project_id>/` 子树与根级文件同时存在；提交携带 `Doc-Connector-Project`、`Doc-Connector-Machine`、`Doc-Connector-Version` trailers。
  4. 本地无变化时 `push` 为空操作（不产生空提交）退出 0；其他前缀造成的非快进自动 fetch、重建并重试（最多 2 次），同前缀竞争则拒绝并提示 `pull`；只有推送成功后才推进基线，失败时保留 pending 供诊断。
  5. `push --dry-run` 与 `pull --dry-run` 打印将执行的动作，工作区、state 文件与远端 ref 全部字节不变。
**Plans**: 5 plans

Plans:
- [ ] 04-01: 冲突登记与产物：`.theirs.<machine>` 命名与 `.2`/`.3` 去重、超长名回退、`unresolved` 集合单一来源、扫描与建树排除 `*.theirs.*`
- [ ] 04-02: push 建树：blob 导入 → 临时 `GIT_INDEX_FILE` 的 `write-tree` 得 `<pid>` 子树 → `ls-tree`/`mktree` 拼接新根树 → `commit-tree`（含 trailers）→ 写 `refs/doc-connector/pending`
- [ ] 04-03: 推送与基线：`--force-with-lease` 与 `push --porcelain` 解析、no-op 检测、`clobber`/`unresolved` 闸门、竞争重试（重新 fetch + 重扫校验 + 退避）、成功后推进基线、启动时核对 pending
- [ ] 04-04: dry-run 管线：push/pull `--dry-run` 全链路只读（`hash-object` 不带 `-w`、`write-tree --missing-ok`、`mktree --missing`），输出计划且不写 state 或 ref
- [ ] 04-05: `push --resolve <path>` 与 modify/delete 冲突：只接受当前 `unresolved` 内路径，覆盖远端并删除产物；删除冲突保留本地决定、列出冲突并退出 1

### Phase 5: 历史、硬化与发布
**Goal**: 补齐离线历史与恢复、跨平台与路径边界硬化、体积门与三平台发布流水线，使 v1.0 成为可分发、可离线审计的单二进制。
**Mode:** mvp
**Depends on**: Phase 4
**Requirements**: HIST-01, HIST-02, HIST-03, CONF-04, GUARD-05, GUARD-08, PLAT-02, PLAT-03
**Success Criteria** (what must be TRUE):
  1. 断网状态下 `log` 从本地镜像打印时间、机器与提交摘要，`log --path <path>` 只显示影响该路径的提交。
  2. `restore <commit> <path>` 在该路径存在未同步改动时拒绝，`--force` 才覆盖；恢复结果是一次普通本地变更（不自动提交）；`restore --force <path>` 同时删除对应 `.theirs.*` 产物。
  3. 大小写碰撞或 NFC/NFD 归一化冲突的路径使整次操作以退出码 3 拒绝并列出全部冲突路径；Windows 保留名与超长路径在 Windows 上以可行动消息拒绝。
  4. 单文件超过 100MB 拒绝同步，超过 50MB 警告并在 `--force` 下继续。
  5. 三平台 CI 矩阵跑通同一套 testscript 用例，GoReleaser 产出 linux/darwin/windows 的 amd64+arm64 归档与校验和。
**Plans**: 5 plans

Plans:
- [ ] 05-01: `log`：离线读本地镜像、时间与机器 trailer 展示、`--path` 路径过滤
- [ ] 05-02: `restore`：`<commit> <path>` 默认拒绝覆盖未同步改动、`--force` 覆盖并解除对应冲突记录、不自动提交
- [ ] 05-03: 路径与平台边界：大小写碰撞、NFC/NFD、Windows 保留名与 MAX_PATH、文件模式归一化与 CRLF 往返，拒绝消息给出可行动下一步
- [ ] 05-04: 体积门：>100MB 拒绝、>50MB 警告与 `--force` 放行；同步到测试夹具与错误消息
- [ ] 05-05: CI 三平台矩阵（测试 + 构建）与 GoReleaser v2 配置：六平台静态归档、校验和、Homebrew cask

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. 契约、身份与基础设施 | 0/4 | Not started | - |
| 2. 隔离护栏与 init | 0/4 | Not started | - |
| 3. 状态与干净拉取 | 0/5 | Not started | - |
| 4. 冲突与推送 | 0/5 | Not started | - |
| 5. 历史、硬化与发布 | 0/5 | Not started | - |
