# Phase 8: 跨仓库聚合 - Context

**Gathered:** 2026-10-22
**Status:** Ready for planning
**Mode:** Autonomous smart-discuss — recommended answers accepted without a live round-trip.

<domain>
## Phase Boundary

一个命令看到并驱动多个已初始化仓库：`status --root <dir>` 汇总、`sync --root <dir>` 串行批量。**不引入 workspace 容器或全局配置文件**：workspace 就是那个目录参数。同时补齐 v1.0 遗留的写锁缺口——`push`/`pull` 至今不取项目锁，而 WORK-02 需要区分 `locked` 与 `failed`。

</domain>

<decisions>
## Implementation Decisions

### 发现（WORK-01）
- `--root <dir>` 必需；只扫描该目录本身与深度 1、2 的子目录（深度 2 = 根下的孙目录），不跟随符号链接
- 判定「已初始化」= 存在 `<repo>/.git/doc-connector/config.yaml`；`.git` 可能是文件（worktree/submodule），因此按路径存在性判断，不假设目录
- 跳过名字为 `.git`、`node_modules`、`vendor`、`.venv` 的目录
- 发现集合按路径排序；有 `.git` 但无 config 的仓库计入 `skipped`（不静默消失）
- 根目录不存在 → 退出 2（用法错误），不是空报告

### 汇总（WORK-01）
- 逐仓调用现有 `app.Status`（进程内、串行），复用三方分类器；不新建第二套判定
- 人类输出每仓一行：`<name>  <project>  <state>  a/b  conflicts  <verdict>`；末尾 `summary:`
- `--json` 输出 `{root, repos:[{name,path,project_id,state,ahead,behind,conflicts,verdict,error}], summary{...}}`
- 单个仓库失败不终止整体；`failed` 与 `locked` 分列

### 批量同步（WORK-02）
- 逐仓串行、每仓重新 fetch；**禁止并发**（共享私有远端的连续推进会把 lease 重试打满）
- 判定表：`clean` → `current`；`behind` → pull → `pulled`；`ahead` → push → `pushed`；`diverged` 或存在冲突 → `blocked`（绝不 resolve、绝不写 `.theirs.`）；pull 返回冲突 → `blocked`；错误 → `failed`
- `locked`（另一进程持锁）单独分类，不算失败也不算 blocked
- 退出码取最坏值：等级 0<1<2<3<4，130 仅中断
- `--dry-run` 只跑 `status`（含 fetch 到镜像缓存，但不推进 base、不写 journal、不写副本）

### 写锁缺口
- `push`/`pull` 在 `Load` 之后、任何写入之前取 `state.Acquire(ctx, gitDir, "push"|"pull", machine)`，结束时释放；锁失败沿用 `state.ErrLocked`（码 3）
- `status`/`log`/`restore`/`diff` **不取锁**（只读或只写镜像缓存）；这是对 STACK 设计的一处有意收窄，理由写进 VERIFICATION
- 锁只覆盖单仓，批量不会自我死锁

### Claude's Discretion
- `verdict` 词表、summary 的具体计数字段、发现深度是否可配置
- 批量模式下是否需要 `--fail-fast`

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `app.Status(ctx, env, StatusOptions)` 返回完整 `StatusResult`（含 remote/base/counts/conflicts）
- `app.Pull` / `app.Push` 已是幂等命令级 API，批量只需调度
- `state.Acquire`/`Release` 与 `ErrLocked`（`internal/state/lock.go`），`init` 已是唯一调用方
- Phase 6 的 `Envelope` + `emit` 让 `--json` 是纯渲染层

### Established Patterns
- 退出码闭集与 `coded` 接口；`Wrap(err, fallback)` 保留域错误码
- CLI 薄壳：命令解析 flag → 调 app → 渲染
- 每个 `errf` 必须带 hint

### Integration Points
- `internal/cli/cli.go` 的 dispatch 需要 `sync` 分支；`status` 增加 `--root`
- `app.Load` 以 `env.Cwd` 为锚点，批量需要每仓构造 `Env{Cwd: repoPath}`
- push/pull 取锁要在 `Load` 之后（`Load` 需要 gitDir）；`recoverJournal` 已在 push/pull 开头调用，顺序应为 取锁 → 恢复 journal

</code_context>

<specifics>
## Specific Ideas

- 批量报告必须能回答「哪个仓库需要我动手」，所以每行都要有 verdict，而不只是 state
- 发现规则要能解释：报告里给出扫描的根与跳过的仓库数

</specifics>

<deferred>
## Deferred Ideas

- `--all-projects`（无根目录的全局登记）：需要全局状态，明确不做
- 并行批量：与 lease 语义冲突
- workspace 级 `log`/`restore`：先用 `cd` 到仓库，聚合价值低

</deferred>
