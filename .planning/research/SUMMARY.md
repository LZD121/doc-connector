# Project Research Summary

**Project:** doc-connector
**Domain:** project-scoped private document synchronization over a shared personal git repository (single-binary Go CLI)
**Researched:** 2026-10-22
**Confidence:** HIGH

## Executive Summary

`doc-connector` 不是文件同步平台，而是一层**带护栏的 git plumbing 编排器**：文档只存在于项目内的 `private-docs/`（被 `.git/info/exclude` 隐藏），真正的仓库是 `.git/doc-connector/store.git` 这个裸镜像，项目身份由主仓库根提交推导出的 `<project_id>/` 前缀表示。真正的竞品不是 Syncthing 或 git-lfs，而是每个人都会写的那份「裸仓库 + 一行 exclude」脚本；产品价值恰恰是那份脚本缺的东西：确定性身份、零污染保证、共享私有仓库的子树拼接、三方比较，以及本地优先的冲突副本。研究（`FEATURES.md`）确认这个组合在既有工具中无人覆盖，因此功能边界应当围绕它收紧，而不是向外扩张。

推荐做法已在 `ARCHITECTURE.md` 中逐条落到 git 2.47.3 实测：单一 Go 二进制 shell out 到系统 git ≥ 2.30；所有文档侧 git 操作都在裸镜像上、用临时 `GIT_INDEX_FILE` 完成，从不 checkout、从不创建工作树；`push` 把本地目录整体哈希成 blob → 临时 index `write-tree` 出子树 → `ls-tree`/`mktree` 只替换远端根树里 `<project_id>` 一个条目 → `commit-tree` 到刚 fetch 的 tip 上 → `--force-with-lease` 追加推送；基线不是文件清单，而是 `state.synced_commit`，其 `<pid>` 子树恒等于「双方上次达成一致的内容」；`status`/`push`/`pull` 共用同一个纯函数 `Classify(B,L,R)`；每个写命令之前跑同一组不变量检查。栈的选择同样收敛：Go 1.26 起步（用 1.27.3 构建）、stdlib `flag`、`go.yaml.in/yaml/v3` v3.0.5、`gofrs/flock` v0.13.1、`testscript` v1.16.0、GoReleaser v2.18.3，无 CGO。

**最大风险是隔离被悄悄打破，而不是同步算错。** 两个具体形态：团队仓库的 `.gitignore` 里一条 `!private-docs/` 反选会压过 `.git/info/exclude`（ignore 优先级是 `.gitignore` > `info/exclude`），下一次 `git add -A` 就把私密文档提交进团队历史；以及浅克隆 / 多根提交 / 非默认分支让根提交推导出另一个 `project_id`，文档被推进私有仓库的第二个目录，两边都「成功」、都没有报错。缓解手段是同一个：护栏不问「是否被 ignore」，而问「是否被**我们那条规则** ignore」——`check-ignore -v -n -z --no-index` 判定来源必须正好是 `.git/info/exclude`；身份侧则对浅克隆、多根、非默认分支一律硬拒绝，只允许显式 `--project-id` 越过。

## Key Findings

### Recommended Stack

版本均于 2026-10-22 从 Go module proxy 与官方发布页核对，plumbing 序列在 git 2.47.3 上实际跑通。三条硬约束驱动了全部选择：单静态二进制（禁 CGO）、跨三平台、以及必须复用用户已有的 SSH agent / credential helper / 代理 / `insteadOf` 配置。因此 git 访问只能 shell out；配置格式用 YAML 是为了人类可读可注释，状态一律 JSON 以保证原子写与严格解析；`os.Root` 负责把文档目录的读写钉死在子树内，不需要 glob 引擎。

**Core technologies:**
- **Go 1.26.0（`go.mod` 下限）/ 用 1.27.3 构建** — 语言与运行时；1.26 是当前仍受支持的最老版本，`x/term`、`x/sys` 已要求 1.26.0，`os.Root` 的 Rename/WriteFile 需要 1.25+
- **stdlib `flag`** — 6 个扁平子命令、约 10 个 flag，零依赖、退出码与帮助文本完全自控，`testscript` 金文件稳定
- **`go.yaml.in/yaml/v3` v3.0.5** — 隐藏配置 `.git/doc-connector/config.yaml`；YAML 官方接手后的维护线，`gopkg.in/yaml.v3` 已冻结在 2022 年的 v3.0.1
- **系统 `git` ≥ 2.30**（实测 2.47.3）— 全部仓库 I/O：`hash-object`、`update-index`、`write-tree`、`mktree`、`commit-tree`、`update-ref`、`rev-parse`、`check-ignore`、`ls-remote`、`cat-file --batch`
- **`github.com/gofrs/flock` v0.13.1** — 跨进程单写者锁；Unix `flock(2)` / Windows `LockFileEx`，进程死亡由内核释放，无需陈旧锁清理
- **`rogpeppe/go-internal/testscript` v1.16.0** — 仅测试引用的端到端 CLI 测试，对真实本地裸远端跑脚本
- **GoReleaser v2.18.3 + GitHub Actions** — 六平台静态矩阵、校验和、Homebrew cask（v2.10 起 `brews:` 已弃用）、Scoop
- **go-git 被拒绝（一行理由）：** 它不支持 `commit-tree`/`update-index`/`update-ref`/`rev-parse`/`check-ignore`，且**从不执行 credential helper**（维护者 2025-02-03 明确确认），v6 仍是 beta —— 这正好抹掉了「复用系统 git」的全部理由。

### Expected Features

`FEATURES.md` 的结论是：表 stakes 全是「命令 + 让命令值得信任的安全契约」，差异化来自四个结构性选择，其余一律推后。v1 切割线是一句话：**每条能写入或删除文档的路径都被护栏包住、都做三方分类、都失败得响亮。**

**Must have (table stakes):**
- `init` 引导与收养（remote 绑定、身份推导/覆盖、store 引导、已存在前缀的 adopt、幂等重入）
- 隐身 exclude 写入器 + 写前污染护栏（未被跟踪、确实被 ignore、`project_id` 三向一致、docs 非符号链接）
- `status` 分歧摘要 + 逐文件 `A/M/D/C`（按内容 OID 比较，非时间戳；`--offline` 与 fetch 时间）
- `push`（自有前缀子树、拼接、追加式提交、无变化 no-op、竞争重试、落后即拒绝）
- `pull`（干净增删改应用、原子写、冲突保留本地、基线与 journal 联动）
- `log`（项目范围内、机器 trailer、`--path` 过滤）与 `restore <path> [--from] [--to]`（默认不覆盖）
- 冲突副本 `<name>.theirs.<machine>` + `push --resolve <path>`；删除传播（modify/delete 永不擦除）
- 机器身份、`--dry-run` 全套、稳定退出码 `0/1/2/3/4/5`
- 内置垃圾忽略表 + `*.theirs.*` + `.git/`；字节精确 blob、可执行位、拒绝符号链接、50 MB 警告 / 100 MB 拒绝
- 路径安全（`..`、绝对路径、Windows 保留名、大小写碰撞）与单实例锁
- 三平台行为、多机流程、任意 git 远端 + 系统凭据（内嵌凭据 URL 警告）、`--help`/`--version`、全非交互

**Should have (differentiators):**
- 根提交派生的确定性身份（克隆到任何地方都是同一个 12-hex，写一次即永久）
- 主仓库零污染保证（唯一允许的写是 `.git/info/exclude` 与 `.git/doc-connector/`，集成测试用前后快照断言）
- 共享多项目私有仓库（只替换自己那一个顶层条目，竞态来自别的项目时在最新 tip 上重建）
- 诚实的内容哈希状态（`touch` 不产生假改动，同尺寸编辑不漏）
- 本地优先的机器后缀冲突副本（永不覆盖、永不自动合并文本）
- 离线可审计的历史（提交带 `Doc-Connector-Project/Machine` trailer，`log` 不联网）
- 身份在项目移动/重命名后不变

**Defer（v2+，明确不建）:**
- 监听/守护进程/定时器（会看到编辑器半写，把确定性 CLI 变成要托管的进程）→ v1.x 只允许用户自建 alias/cron 调幂等 `push`
- 端到端加密（密钥分发与丢失成为新故障源；私库 ACL 就是边界）、密钥管理、自动创建私有远端、GUI/移动端、git-lfs、云存储后端、自动文本合并、团队仓库里的隐藏 ref、`push --force`、git hooks 安装、`sync` 合并命令、多项目共用同一 docs 目录、`skip-worktree` 方案
- v1.x 而非 v1：`.docignore`、`--json`、`doctor`、shell 补全、符号链接支持、NFD/NFC 归一化、`restore --stdout`、进度条、`push -m`、`status --all-projects`、`store gc`

### Architecture Approach

十个组件、五条构架模式、六条协议不变量（团队仓库只读；不存在第二个工作树；任何 index 操作都设 `GIT_INDEX_FILE`；ref 只用 lease 更新；基线只在成功后推进；不丢内容）。依赖方向单向：`cli → app → {sync, history, init} → {trees, gitstore, fsx} → {config, state, ignore, project}`，`gitstore` 是唯一能生成 `exec.Command("git", …)` 的包。

**Major components:**
1. **CLI dispatch** — argv → Request、flag 校验、退出码映射、人类可读输出，每个命令一个 `Run(ctx, Request) (ExitCode, error)`
2. **Repo identity** — 定位工作树/公共 git 目录、根提交推导、`project_id` 派生、linked-worktree 拒绝
3. **Config / State** — `config.yaml`（YAML，`KnownFields`）、`state.json` / `journal.json` / `fetched.json` / `lock`（JSON，temp+fsync+rename）
4. **Git store** — 唯一执行 git 的地方：env 清洗、对象导入、fetch/push/ref、blob 读取、错误分类 `GitError{Args, Code, Stderr}`
5. **Tree builder** — 扫描结果 → 项目子树 → 与远端根树拼接 → 新根树
6. **Sync engine** — 三方分类、动作计划、apply、重试循环、基线推进
7. **Conflict resolver** — `.theirs.<machine>` 命名/去重/长度回退、`unresolved` 集合、`--resolve` 语义
8. **Filesystem guard** — `os.Root` 封装、路径校验、原子写、空目录清理、锁
9. **Ignore filter** — 内置垃圾表 + `*.theirs.*` + `.doc-connector-conflicts/`，纯函数 `Match(relPath)`
10. **History service** — `log`、`restore`、`cat-file --batch` 流式 blob 读取；只读镜像，可离线

**Push 子树拼接协议（实现要点）：** 扫本地 → `hash-object -w --no-filters` 入镜像 → 临时 index `read-tree --empty` + `update-index --index-info` + `write-tree` 得子树 → `ls-tree -z <REMOTE>` 去掉 `<pid>` 一项 + `mktree -z` 得新根树 → 根树等于远端根树即 no-op 退出 0 → 否则 `commit-tree Troot -p <REMOTE>` → 写 `refs/doc-connector/pending` → `push --porcelain --force-with-lease=refs/heads/<branch>:<expect>`。`--dry-run` 用不带 `-w` 的 `hash-object` 与 `write-tree --missing-ok` / `mktree --missing`，不建提交、不写 ref、不推送。被 lease 拒绝时**必须重新 fetch**（旧期望值会一直 `stale info`），重扫校验 `L` 未变后在新 tip 上重建，最多 3 次（100/300/900ms + jitter），耗尽退出 4。

**基线定义：** `state.synced_commit` 这个提交本身，基线树恒等于 `rev-parse <synced_commit>:<pid>`；`baseline_tree` 只是缓存，加载时校验，不符以提交为准。push 成功后基线=新提交；pull 干净后基线=被拉取的远端头（即使留了冲突副本，副本在约定内容集之外）。绝不把「我最后一次提交」当基线。

**三方分类：** `Entry{Mode, OID}`，`eq` 要求 Mode 与 OID 都相同（`chmod +x` 算修改；Windows 在分类前把同 OID 不同 Mode 归一化到基线/远端 Mode，纯内存不落盘）。15 种组合覆盖 `unchanged / local-add / remote-add / add-add / converged / local-mod / remote-mod / both-modified / both-same / local-delete / remote-delete / both-deleted / delete-modify / modify-delete`。push 只用一条判据：`clobber = {p | !eq(R[p],B[p]) && !eq(L[p],R[p])}` 为空才继续（允许「远端改了、本地恰好也改成同样内容」）；pull 只有 remote-add / remote-mod / remote-delete 会动文件系统，其余冲突只增加冲突记录。

### Critical Pitfalls

`PITFALLS.md` 共 43 条、1291 行，全部绑定到这套具体机制。会丢数据或破坏隔离的前五条：

1. **身份漂移（P1/P3/P4/P5）** — 浅克隆的 graft 边界让 `rev-list --max-parents=0` 返回 tip，于是第二个 `<project_id>/` 目录被凭空创建，无人报错。**护栏：** 每个命令（不只 `init`）都跑 `rev-parse --is-shallow-repository`，并在 `.git/shallow` 存在时同样拒绝；多根提交 = 硬错误并列出所有根与可达分支；拒绝非默认分支的根；只允许显式 `--project-id` 越过；`lower(<root OID>)[:12]` 写一次，配置校验 `^[0-9a-f]{12}$` 且 `id_source` 三向一致。
2. **文档泄漏进团队仓库（P8/P9/P12）** — `.gitignore` 的 `!private-docs/` 反选优先级高于 `info/exclude`；而 `check-ignore` 对已跟踪文件返回 1、多路径时 0 只表示「至少一个命中」，`rc != 0` 的朴素读法会误判。**护栏：** `check-ignore -v -n -z --no-index --stdin` 解析四字段 NUL 记录，要求来源正好是解析后的 `.git/info/exclude`，否则连文件行号和模式一起打印并拒绝所有写命令；已跟踪检测单独用 `ls-files -z -- <docs>/` 而非 `check-ignore`；集成测试在每个命令前后比对 `.git/index` 哈希与 `for-each-ref`。
3. **推送失败但基线已推进（P19/P37）** — 对象上传了、ref 被拒或连接在服务端更新后断开，`status` 却报 clean，下一次 pull 把每个远端文件看成「本地修改」。**护栏：** 基线只在权威读取之后推进（`ls-remote <url> <ref>` 等于被推 SHA，或其后代为它）；保留 `pending{commit,parent,remote_head,started_at}` 并在每次启动时核对；`push --porcelain` 逐行区分 `ok` 与 `!`，传输错误单独归类；state 原子写。
4. **半写 pull 与「目录不见了 = 全删」（P29/P32/P31）** — 一次 `mv` 加一次 push 就能把语料从所有机器上抹掉；崩溃留下的半应用树配旧基线还会让下一次 apply 基于幻觉。**护栏：** 每个文件 temp→fsync→rename，删除先 rename 进 tmp 回收站；写 journal 后才 apply，失败回滚、崩溃后自动 resume，只有 journal 完整结束才推进基线；docs 目录缺失或不是目录 = 硬错误（`push` 永不自动创建）；删除超过 `max(5, 25%)` 需 `--allow-deletes`，本地文件数为 0 而基线非空需 `--allow-deletes=all`。
5. **冲突副本被同步 / push 覆盖远端前缀（P33/P36）** — `.theirs.<machine>` 就写在扫描目录里，下一次 push 会把它发给每台机器；同理，push 若不以 `clobber` 为空为前提就会擦掉远端同前缀改动。**护栏：** 冲突登记表记录精确路径，扫描器与树构建器同时排除（`*.theirs.*` 作为兜底，后缀是保留命名）；`unresolved` 非空时 push 拒绝退出 1；建树之前先算 `clobber` 并整体拒绝；提交 parent 永远是刚 fetch 的 tip，且先 `merge-base --is-ancestor` 确认基线未被外部改写。

## Implications for Roadmap

`ARCHITECTURE.md` 的构建顺序（exec → identity → config/state → fsx/ignore/scan → trees → gitstore → init → classify → pull → push → log/restore → 跨平台）与 `PITFALLS.md` 的 P1–P6 映射几乎重合。按其粒度合并为 7 个粗阶段；`feat/` 粒度保持粗，一个阶段内可以并行多个 plan。

### Phase 1: Foundation, contracts and release skeleton
**Rationale:** 下游一切都通过 argv 与退出码说话；先定下 0–5 契约与唯一 git 执行缝隙，后续阶段才能用假 git 回放测试、不必先有真仓库。git 版本门与 Go 版本门必须早于任何命令。
**Delivers:** `cmd/doc-connector` 组装层、`internal/cli` 六命令骨架（未实现返回 2）、退出码常量与映射、`--help`/`--version`、`--dry-run` 全局管线、`internal/gitstore` 的 `Runner`（结构化 argv、env 清洗、`GitError` 分类、非交互/超时）、`git --version` ≥ 2.30 门、CI 三平台矩阵 + `testscript` 骨架、GoReleaser v2.18.3 配置与 Homebrew cask。
**Addresses:** 稳定退出码、CLI 人体工学、`--dry-run`（管线）、任意远端（复用系统凭据的机制）。
**Avoids:** P2（仓库/环境误解析）、P23（把 plumbing 输出当行式解析）、P24（无作者身份时在建对象前失败）、P40（凭据/主机键/代理导致的悬挂）。
**Research flag:** 无需研究 —— 全是标准模式，`STACK.md` 已给出精确版本与配置键。

### Phase 2: Project identity, config, state and locking
**Rationale:** `project_id` 写一次即永久，身份拒绝逻辑必须先于 `init` 存在，否则第一次就可能写错目录；config/state/journal/lock 的原子写原语被之后每个阶段复用。
**Delivers:** `internal/project`（`rev-parse` 组合解析、`DeriveID(hex) = lower(hex)[:12]` 纯函数 + 测试向量 + SHA-256 仓库用例、浅克隆/多根/归档包/裸库/linked-worktree/子模块拒绝、`--project-id` 显式路径与 `id_source`、`object_format` 记录）、`internal/config`（YAML、`KnownFields`、拒绝其它 id 格式）、`internal/state`（`state.json` / `journal.json` / `fetched.json`、temp+fsync+rename、Windows rename 重试、`flock` 单实例锁与 PID/host 存活检测）、机器身份（`machine` 配置、默认清洗后的 hostname）。
**Addresses:** 确定性身份、机器身份、单实例锁、多机流程（配置侧）。
**Avoids:** P1, P2, P3, P4, P5, P6, P7, P41, P43。
**Research flag:** 无需研究 —— 拒绝矩阵与命令语义已在 `PITFALLS.md` §Verified 逐条实测；只需把表格变成表驱动测试。

### Phase 3: Isolation guard and `init`
**Rationale:** 这是产品存在的理由，且 `push`/`pull` 必须跑在护栏之后，因此护栏与 `init` 早于任何写路径。`init` 还要一次性决定收养语义（本地有内容、远端前缀也存在时该怎么办），这是后续所有分歧语义的起点。
**Delivers:** exclude 块写入器（受管标记、幂等、无尾换行文件也字节一致、不碰 `.gitignore`）、`check-ignore -v -n -z --no-index` 来源校验、`ls-files -z` 已跟踪校验、所有写命令共用的 guard 流水线、`init`（remote 校验与内嵌凭据拒绝、默认分支探测或 `--branch`、`ls-remote` 探测空远端、空远端引导 / 非空 fetch、docs 目录创建、收养矩阵与 `--adopt-local`、`--force` 重入）、第二台机器流程（clone → init → pull）。
**Addresses:** `init` + 收养、隐身 exclude、写前污染护栏、多机流程、任意远端与凭据警告。
**Avoids:** P7, P8, P9, P10, P11, P12, P13, P35, P39, P41, P43。
**Research flag:** 需要（轻量）—— 收养/分歧决策表（本地有内容 × 远端前缀存在 × 基线缺失 的组合）值得一个短设计 spike；机制已验证，缺的是语义选择，不是资料。

### Phase 4: Sync core — scan, tree builder, fetch, classifier, `status`
**Rationale:** 这是只读阶段，却把三条命令的正确性核心一次性落地：15 行分类表是纯函数、无需磁盘即可验收；`status` 是第一个用户可见价值；`trees` 的拼接协议只能对真实 git 断言 tree OID。放在任何写路径之前，意味着后面的 pull/push 只是「把同一个 Plan 执行掉」。
**Delivers:** `internal/ignore`（内置垃圾表 + 保留后缀 + 精确匹配）、`internal/fsx` 的扫描与 `os.Root` 封装（`hash-object --no-filters --stdin-paths`、可执行位、符号链接拒绝、大小统计）、`internal/trees`（子树构建 + 根层 `ls-tree`/`mktree` 拼接，含空树与整前缀删除）、`gitstore` 的 fetch 与 `cat-file --batch`、`Classify` 十五行表与 `Plan`、`status`（clean/ahead/behind/diverged、逐文件 A/M/D/C、按内容 OID、`--offline` 与 fetch 时间、fetch 失败退回缓存状态并退出 4）。
**Addresses:** `status`、三方分类、诚实内容哈希、离线检视、字节精确、内置忽略表、大小护栏（警告侧）、`--dry-run`（status 之外的计划预览）。
**Avoids:** P21, P22, P23, P25, P26, P27, P28, P30, P37（分类器侧）、P38（一律拒绝 gitlink）。
**Research flag:** 无需研究 —— 协议已实测；本阶段的未知只在测试夹具设计。

### Phase 5: `pull`, journal, conflicts and path safety
**Rationale:** 这是唯一会删除本地文件的路径，风险最高，因此它在 push 之前、在护栏、分类器、原子写都就位之后。冲突语义（本地优先 + 副本 + 显式解决）也在这一阶段定型，push 才能把 `unresolved` 当成硬闸门。
**Delivers:** `Plan → Apply` 两阶段（`--dry-run` 返回真实会返回的码）、远端 blob 先流式落到 `tmp/op-<rand>/`、`journal.json` 与崩溃恢复/回滚、逐文件 temp+fsync+rename、空目录自底向上清理、冲突产物命名与 `.2/.3` 去重、超长路径改写进 `.doc-connector-conflicts/`、`unresolved` 集合与 `status` 的 `C path -> artifact`、路径校验管线（绝对路径/`..`/逃逸/符号链接父级/Windows 保留名/大小写与 NFC 碰撞整批中止）、apply 窗口内被编辑文件的检测。
**Addresses:** `pull`、冲突报告与副本、删除传播（modify/delete 永不擦除）、原子应用、路径安全、`--dry-run` 冲突退出码。
**Avoids:** P25, P26, P27, P28, P29, P32, P33, P34, P35, P38。
**Research flag:** 需要 —— 崩溃恢复语义（journal 状态机、回收站保留策略）与 Windows rename/文件锁行为只能部分在 WSL2/9p 上验证，需在 CI 矩阵上实测后再固化。

### Phase 6: `push` with lease, retry and baseline advance
**Rationale:** push 的风险是「悄悄覆盖」而非「写坏本地」，所以它排在 pull 之后：此时 `clobber` 判据、冲突闸门、基线语义都已存在，可以直接把「远端有、本地没有的版本」变成拒绝。竞争重试是本阶段唯一真正需要设计的状态机。
**Delivers:** `Tsub`/`Troot` 构建 → `commit-tree` → `refs/doc-connector/pending` → `--force-with-lease` 推送、no-op 检测（`Troot == tree(REMOTE)`）、`clobber` 非空即退出 1、`merge-base --is-ancestor` 检测远端历史被改写（退出 1，要求显式处理）、重试循环（重新 fetch + 重扫校验 + 100/300/900ms jitter，耗尽退出 4）、`--resolve <path>` 只接受当前 `unresolved` 内路径、删除预算（`--allow-deletes` / `--allow-deletes=all`）、大小前置拒绝（100 MB，`--allow-large`）、`--dry-run` 严格无副作用（不建提交、不写 ref）。
**Addresses:** `push`、共享多项目仓库拼接、冲突解决（本地优先）、无变化 no-op、删除预算、大小护栏（拒绝侧）。
**Avoids:** P14, P15, P16, P17, P18, P19, P20, P31, P36, P37（基线侧）。
**Research flag:** 需要（轻量）—— 竞争窗口与「响应丢失」的重试状态机、以及 `gc --auto` 与 `pending` ref 的存活保证值得在规划时先跑两个 store 的竞争原型。

### Phase 7: History, recovery, cross-platform hardening and release
**Rationale:** `log`/`restore` 只读镜像、依赖已完成的 store 与路径安全，放在最后不阻塞任何写路径；跨平台行为（Windows 替换语义、模式/CRLF、macOS 归一化）只有在整个写路径存在后才有意义；打包与文档在这一阶段收尾。
**Delivers:** `log`（`%H%x00%aI%x00%an%x00%s`、`--name-only`、`-- <pid>/<subpath>`、机器 trailer、默认远端 tip 而非孤儿本地提交）、`restore <path> [--from <rev>] [--to <dir>]`（默认当前远端 tip、差异时拒绝覆盖、可恢复已删除文件、恢复后 OID 等于远端则自动解除 `unresolved`）、Windows/macOS 收尾（长路径、保留名、文件锁、模式归一化、CRLF 往返、NFD 用例如需推后则给出可行动拒绝消息）、网络错误分类学（认证/代理/`safe.directory`/无 TTY 快速失败）、`status` 显示 pending 而非假 clean、发布与用户文档。
**Addresses:** `log`、`restore`、离线审计、跨平台、`--help`/`--version` 完善、v1.x 触发点的记录。
**Avoids:** P5, P13, P16, P19（展示侧）、P20, P22, P25, P29, P39, P40, P41, P42（文档化边界）。
**Research flag:** 需要 —— Windows/macOS 文件系统与凭证错误分类无法在 WSL2 本地验证，必须在 CI 矩阵上先取得实测数据再确定具体错误映射。

### Phase Ordering Rationale

- **依赖驱动，而非偏好驱动：** identity 必须先于 `init`（prefix 写一次即永久）；guard 必须先于任何写命令；`trees`/`gitstore` 必须先于 `init`（store 引导要 fetch）；`Classify` 必须先于 `pull`/`push`（三条命令共用同一分类器，各自实现会互相矛盾——比一致地错更糟）；`pull` 先于 `push`，因为 push 的安全前提是 `clobber` 与 `unresolved` 语义已经存在。
- **每个阶段都以「可验收的观测」结束：** Phase 1 退出码表、Phase 2 身份测试向量、Phase 3 五夹具表 + exclude 字节一致、Phase 4 十五行分类表 + tree OID、Phase 5 kill-after-N 收敛、Phase 6 两个 store 竞争、Phase 7 三平台矩阵。这样护栏与协议不变量不会在后期被「优化掉」。
- **护栏写成第一等组件而非横切关注点：** `Guard` 与 `Classify` 都是纯函数 + 表驱动测试，五条最危险的 pitfall 因此可以逐条对应到一个测试，而不是一句注释。
- **与 pitfall 阶段映射对齐：** `PITFALLS.md` 的 P1–P6 与本文 7 个阶段是同一分解的细化，任何阶段规划时可直接查表取本阶段的护栏与验证门。

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 3:** 收养/分歧决策矩阵与空远端引导的边界组合需要设计确认（机制已验证，语义未定）。
- **Phase 5:** journal 状态机、崩溃恢复与 Windows 替换/锁定语义，需在 CI 矩阵上取得实测后固化。
- **Phase 6:** 竞争重试状态机、响应丢失的收敛判定、`pending` ref 与 `gc` 的存活保证，建议先做双 store 竞争原型。
- **Phase 7:** 三平台文件系统行为与凭证/代理错误分类，WSL2 本地不可代表。

Phases with standard patterns (skip research-phase):
- **Phase 1:** CLI 骨架、退出码、`exec` 缝隙、GoReleaser 配置，全部有官方文档与既定模式。
- **Phase 2:** 身份推导与原子写，`PITFALLS.md` §Verified 已给命令与观测输出，直接转测试即可。
- **Phase 4:** 树拼接与三方分类，协议逐条实测过，剩下的是夹具设计。

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | 版本取自 Go module proxy 与官方发布页；plumbing 序列在 git 2.47.3 上实际执行；go-git 的能力缺口有官方 COMPATIBILITY 与维护者声明 |
| Features | HIGH | 竞品机制对照一手文档核实；复杂度/工期分级为 MEDIUM（规划估算，非承诺） |
| Architecture | HIGH | 每条协议不变量的命令语义均对照官方文档并在 git 2.47.3 上实测 |
| Pitfalls | HIGH | 43 条全部绑定本机制并有 §Verified 附录（命令 + 观测输出）；阶段排序为 MEDIUM（撰写时尚无 ROADMAP.md） |

**Overall confidence:** HIGH

### Gaps to Address

- **身份公式的文档残留：** `ARCHITECTURE.md` §"Project identity" 仍留有被作废的 `sha256(root_hex)[:12]` 段落与其测试向量（约 232–245 行），而同文件后续流程已写 `pid = lower(root_hex)[:12]`。冻结规则唯一：`lower(<root commit OID>)[:12]`，无第二次哈希。Phase 2 编码前必须删除旧段落与旧向量，只保留 `743c420a8e7b` 这类直接截断的向量。
- **冲突登记表的命名与位置：** `ARCHITECTURE.md` 用 `state.unresolved`，`PITFALLS.md` 用 `conflicts.json`。建议统一为单一原子写的 state 文件中的一个集合，Phase 5 前定稿，避免 push 闸门读错来源。
- **`--resolve` 的退出码：** `FEATURES.md` 规定冲突未解时 push 退出 1，`PITFALLS.md` §33 写作退出 2。以功能规范为准（1 = 需要用户决策），并在 `--help` 与测试中固定。
- **Windows 语义不可本地验证：** WSL2/9p 上的 `core.ignorecase=true`、`core.filemode=false` 与真实 Windows 不同；重命名原子性、`LockFileEx`、模式归一化必须在 CI 矩阵取得数据后定稿，不要在本地「验证通过」后宣布完成。
- **恢复入口是否进 v1：** `PITFALLS.md` 的恢复表提到 `refs/doc-connector/backup/<ts>` 与 `--rebase-baseline`，`FEATURES.md` 的 v1 范围没有它们。需明确是「v1 实现」还是「文档化的人工恢复流程」，否则 Phase 6 的拒绝分支没有出口。
- **规模假设未压测：** 首个瓶颈是本地全量 hash（每次 O(文件数×大小)）。v1 不加 mtime 缓存，但需在 500–5k 文件量级实测一次，再决定触发点。
- **`PITFALLS.md` 体量：** 1291 行，**不要整读**。按本文的阶段映射与各 pitfall 末尾的 "Phase to address" 定位，每阶段规划时只取其归属条目；§Verified 只在需要复现实测时查。
- **延后项的拒绝消息：** 符号链接、NFD/NFC、`.docignore` 在 v1 是「拒绝 + 可行动提示」，需要确认提示里给出的替代路径确实存在（例如「把该文件移出 docs 目录」）。

## Sources

### Primary (HIGH confidence)
- <https://git-scm.com/docs/git-commit-tree>、`git-mktree`、`git-update-index`、`git-ls-tree`、`git-cat-file`、`git-hash-object`、`git-push`、`git-check-ignore` — plumbing 契约与 `check-ignore` 的 0/1/128 语义
- <https://go.dev/doc/devel/release> 与 <https://go.dev/dl/> — `go1.27.3` 为当前稳定版，"支持到再出现两个新大版本" 的策略决定 1.26 下限
- <https://pkg.go.dev/os#Root>、<https://pkg.go.dev/os#Rename> — 子树受限遍历；非 Unix 平台 Rename 非原子的官方说明
- <https://pkg.go.dev/go.yaml.in/yaml/v3>、<https://github.com/yaml/go-yaml> — v3.0.5 与 `,inline` 的未知键往返
- <https://github.com/go-git/go-git/blob/master/COMPATIBILITY.md>、<https://github.com/go-git/go-git/issues/1420> — go-git 缺失的 plumbing 与「不支持 credential helper」的维护者声明
- <https://pkg.go.dev/github.com/gofrs/flock>、<https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript>、<https://goreleaser.com/customization/publish/homebrew_casks/> — 锁语义、脚本测试、`brews:` 已弃用

### Secondary (MEDIUM confidence)
- `PITFALLS.md` §Verified Mechanics — git 2.47.3 / Linux-WSL2 上的实测表（`check-ignore` 返回码、`!private-docs/` 反选生效、9p 的 `core.ignorecase`、`commit-tree` 无身份报错等）
- `STACK.md` 的本地验证段落 — `read-tree --empty` + `update-index -z --index-info` + `write-tree` + `commit-tree` + `update-ref` 全链路跑通；空远端 fetch 需 glob refspec
- `ARCHITECTURE.md` 的并发表与退出码映射 — 竞争场景与错误分类的对应关系

### Tertiary (LOW confidence)
- `FEATURES.md` 的复杂度/工期分级（S/M/L）与竞品 fit 评分 — 规划估算与主观打分，随实现修正
- 各阶段的粒度与合并方式 — 本文按 `ARCHITECTURE.md` 构建顺序合并，尚未与实际开发节奏对齐

---
*Research completed: 2026-10-22*
*Ready for roadmap: yes*
