# Phase 6: 可观测性与诊断 - Context

**Gathered:** 2026-10-22
**Status:** Ready for planning
**Mode:** Autonomous smart-discuss — recommended answers accepted without a live round-trip (grey-area tables are reproduced in the phase report).

<domain>
## Phase Boundary

`status`/`push`/`pull` 增加 `--json`（单一文档、稳定 schema、失败也有文档），新增 `doctor` 只读诊断命令，并把「错误必须带可执行 hint」从既有 32 处提升为全覆盖契约。不新增任何同步语义，不改写 app 的判定逻辑——`--json` 是渲染层，`doctor` 是只读检查器。

</domain>

<decisions>
## Implementation Decisions

### JSON 信封
- 顶层恒为 `{"schema_version":1,"command":"<verb>","ok":true|false, ...payload}`
- 失败时载荷为 `{"schema_version":1,"command":"…","ok":false,"error":{"code":"<closed set>","message":"…","hint":"…"}}`；`hint` 为空则省略键
- `code` 闭集字符串：`conflict`(1) / `usage`(2) / `precondition`(3) / `remote`(4) / `interrupted`(130)；未知码回落 `internal`
- 每次调用 stdout 恰好一个 JSON 文档并带尾随换行；诊断与 warning 仍走 stderr；`--json` 下 stdout 不含 ANSI
- `--json` 不改变退出码：退出码仍由同一个 `coded` 错误决定

### 载荷形状
- snake_case；DTO 定义在 `internal/cli`，不给 `app` 结果结构体加 json tag（app 与线上格式解耦）
- `status`：`project{id,docs_dir}`、`remote{url,branch,tip,checked,fetched_at,fetched_age_seconds,fetch_error}`、`base{commit,tree}`、`state`、`counts{ahead,behind,conflicts}`、`files[{path,status,local_oid,base_oid,remote_oid}]`（仅变更路径，与人类输出同一集合）
- `push`：`dry_run,no_op,tracked,added,modified,deleted,commit,tree,parent,resolved[]`
- `pull`：`dry_run,writes,deletes,remote_tip,conflicted[],applied[{path,status}]`
- JSON 里 OID 用完整值；人类输出保持 12 位短形式
- 部分成功（fetch 失败但有缓存结果）仍输出载荷，`remote.fetch_error` 非空且退出码 4

### doctor
- 检查顺序固定：git 版本 → 仓库发现 → 隔离（排除块来源 + 未跟踪）→ 配置/状态一致性 → 锁 → 远端可达 → 遗留产物
- 每项 `{id,status:ok|warn|fail,detail,hint}`；`doctor` 只读——除「尝试取锁再释放」外不写任何文件
- 退出码：任一 `fail` 且属远端类 → 4；任一 `fail` 属隔离/身份类 → 3；仅 `warn` → 0
- `--json` 输出 `checks[]` + `summary{ok,warn,fail}`

### hint 契约（OBS-03）
- 每个 `errf(...)` 构造点都必须链一次 `withHint`；hint 只能陈述工具已验证的状态或可直接执行的命令
- 测试用表驱动断言：每个可达错误码路径的 `HintText()` 非空
- `--json` 的 `error.hint` 与人类输出的 `hint:` 行同源（都取 `HintText()`）

### Claude's Discretion
- 具体键顺序、`fetched_age_seconds` 的取整方式、doctor 检查项 id 命名
- 是否复用现有 `warnWriter` 还是新增 `diag` 通道（只要 stdout 干净）

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `cli.parseFlags` / `noPositional` / `Wrap` / `coded`（`internal/cli/exit.go:49-65`）已把错误码与 hint 保到 `Run`
- app 全部命令**只返回结构体，不打印**（除 warning 走 `env.Stderr`）——`--json` 纯渲染层，无需重构
- `gitstore.EnsureMinimum`、`project.GuardAll`、`project.ExcludePath`、`store.RefTip`、`state.LoadFetched`、`state.LoadJournal`、`state.Dir` 都是 doctor 的现成料
- `project.TakeSnapshot`（`internal/project/guard.go:116`）可用于「零污染」自检

### Established Patterns
- 错误：`errf(code, …).withHint(…)`；`wrapErr` 保留下层码与 hint
- 退出码闭集与 `coded` 接口的映射链：cmd → `Wrap` → `Run` 的 `errors.As`
- 帮助文本是手写常量（`internal/cli/help.go`），新命令需同步加常量

### Integration Points
- `internal/cli/cli.go` 的 `dispatch` switch 需要新增 `doctor` 分支
- `StatusResult.FetchError` 不可 JSON 化，且错误码不在结果结构体内 → 错误文档必须在 cli 层由 `coded` 错误拼装
- 锁持有者信息只有失败路径（`internal/state/lock.go:66-80`）与未导出的 `lockInfo`；doctor 需新增只读探测（取锁失败后解析锁文件）

</code_context>

<specifics>
## Specific Ideas

- 人类输出保持 v1.0 逐字节不变（脚本已依赖），JSON 是新增通道而非替换
- `doctor` 的 `detail` 要带具体路径/行号，而不是「某处配置有问题」

</specifics>

<deferred>
## Deferred Ideas

- `log --json` 与 `restore --json`：本阶段只承诺 status/push/pull；其余命令在 Phase 7/8 复用同一信封时再补
- 颜色/`--no-color`/`--quiet`：v1.0 未实现且与 JSON 无冲突，留待有需求时
- 进度指示（大 pull/push）：无真实 >100MB 场景

</deferred>
