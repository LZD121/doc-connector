# Phase 7: 本地控制与历史视图 - Context

**Gathered:** 2026-10-22
**Status:** Ready for planning
**Mode:** Autonomous smart-discuss — recommended answers accepted without a live round-trip.

<domain>
## Phase Boundary

四个本地控制能力：`.docignore`（不提交的本地忽略）、多文档目录映射（一个项目 → 多个子树）、可选符号链接（默认仍拒绝）、`diff` 历史视图。核心同步语义（三方分类、本地优先冲突、lease push）不变；变化集中在扫描层（忽略/链接）、配置层（目录列表）与路径映射层（prefix → 目录）。

</domain>

<decisions>
## Implementation Decisions

### .docignore (LOCL-01)
- 位置：每个被映射目录的根；文件名固定 `.docignore`，不提交、不推送、不进 status（自动加入自身忽略集）
- 语法：**不是**完整 gitignore。只支持两种模式——basename（`*.tmp`、`Thumbs.db`）与目录前缀（`build/`、`drafts/`）；`#` 开头为注释，空行忽略
- 匹配：与内置垃圾列表叠加（内置优先，用户不可取消）；大小写不敏感，与既有 `ignore.Filter` 语义一致
- 作用面：status / push / pull 三处同一 `Filter`；pull 不得删除被忽略的本地文件

### 多文档目录 (LOCL-02)
- config schema 升到 2：`docs_dirs: [{path, prefix}]`；`prefix` 为空表示 `<project_id>/<rel>`（v1.0 布局），非空表示 `<project_id>/<prefix>/<rel>`
- **v1.0 兼容**：单 `docs_dir` 在 Load 时内存归一化为 `docs_dirs: [{path: docs_dir, prefix: ""}]`，远端路径逐字节不变；下一次 `init`/Save 才写 schema 2
- 校验：至少一项；path 本地相对且互不相同；prefix 非空时必须通过 `fsx.ValidateRel`，且任意两个 prefix 不得相同或互相嵌套
- exclude 块为每个目录各写一行；guard 对每个目录各查一次
- 单目录项目行为必须与 v1.0 逐字节一致（回归测试）

### 符号链接 (LOCL-03)
- 默认仍整次拒绝（与 v1.0 一致）；显式 `--follow-symlinks`（status/push/pull）才开启
- 链接目标必须是文档根内的相对路径；绝对路径、`..` 越界、目录链接一律拒绝并列出违规项
- 同步的是**目标内容**（mode 100644/100755），不是 link 文本；远端永远不出现 120000 条目

### diff (LOCL-04)
- `diff <commit> [path]` 对比该提交的项目前缀与当前工作区，只读镜像、不触网、不写 state
- 人类输出直接用 `git diff --no-color` 的 unified 形式；`--json` 输出 `{path, status, insertions, deletions, patch}` 列表
- `--from` 可选，默认对比工作区；路径过滤限定在项目前缀内

### Claude's Discretion
- 忽略文件的解析细节（行尾空白、`!` 取反不支持但要明确拒绝还是忽略）
- diff 的 patch 截断上限（避免超大二进制刷屏）
- 多目录下 restore/log 的路径消歧提示文案

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `ignore.New(extra)` + `Filter.Match(rel, isDir)` 是唯一过滤入口，`scan.Scan` 只认它
- `scan.Options{Write, Filter, MaxBytes, WarnBytes, Force}`；`fsx.Open` 提供 root 内遍历
- `config.Config` 单 `DocsDir`，`Validate` 已校验本地相对；`applyDefaults`/`Save` 有迁移挂点
- `project.ExcludeBlock(docsDir)`/`EnsureExclude` 单模式；`GuardIgnored` 要求规则来源是 managed 块
- `store.Run(ctx, "log"|"diff"|"show", …)` 是既有的直接 plumbing 先例（`app/log.go:69`）

### Established Patterns
- 路径前缀拼接 `<pid>/<rel>` 在 push/pull/restore/log 各自出现 → 多目录需要一处映射函数
- 尺寸预算 `cfg.Limits.MaxPathChars - len(docsDir) - 1` 按目录计算
- 错误一律 `errf(code, …).withHint(…)`；新增错误必须带 hint（Phase 6 契约）

### Integration Points
- `app.Load` 是唯一装配点：多目录后需要 `[]docsEntry{Path, Abs, Prefix, Root}` 取代单个 `Root`
- `scan.Scan` 需要 prefix 参数或返回 `rel→projectPath` 映射
- `pull` 写盘与 `restore`/`log` 读取都需要 projectPath → (dir, rel) 反查

</code_context>

<specifics>
## Specific Ideas

- 多目录是唯一有迁移代价的项：v1.0 单目录必须逐字节等价，先用回归测试锁死再改
- `.docignore` 明确不做完整 gitignore（FEATURES.md 的 anti-feature 立场：不建 glob 引擎）

</specifics>

<deferred>
## Deferred Ideas

- `!` 取反、`**` 通配：需要 glob 引擎，明确不做
- 目录符号链接：跨目录树形歧义，留待真实需求
- `restore --stdout` / `--to -`：仍延后

</deferred>
