# Phase 10: TUI 汇总屏 - Context

**Gathered:** 2026-10-23
**Status:** Ready for planning
**Mode:** Autonomous smart-discuss — recommended answers accepted without a live round-trip.

<domain>
## Phase Boundary

一个 `tui` 子命令：跨仓库一览 + 冲突分诊。**零 git 调用**——每个动作都调用 `internal/app` 的既有函数。不新增同步语义、不引入 TUI 框架。

</domain>

<decisions>
## Implementation Decisions

### 依赖
- 只加 `golang.org/x/term`（纯 Go、跨平台、STACK 已列为选择，用于 `IsTerminal` + `MakeRaw`）；**不**引入 bubbletea/lipgloss
- 渲染用 ANSI 常量 + 全屏重绘；备用屏（`\x1b[?1049h`）进入/退出

### 屏幕与按键
- 屏 1：仓库列表（name / project / state / verdict），`↑↓`/`jk` 移动，`enter` 进入，`r` 刷新，`q` 退出
- 屏 2：选中仓库的冲突路径，`l` 保留本地（`app.Push --resolve`），`t` 采纳远端（`app.Restore --force`），`esc` 返回
- 动作后重读冲突并刷新汇总，屏幕底部显示一行反馈

### 非 TTY
- 非交互（管道/CI）直接拒绝，码 2，hint 指向 `status --root`——绝不半渲染

### 一致性
- TUI 与 CLI 必须产生同样的结果：测试直接比较动作后的仓库状态（冲突清零、内容、副本清理）
- 架构守卫：`tui.go` 不得出现 `gitstore.`/`exec.Command`

### Claude's Discretion
- 配色、列宽、是否需要滚动

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `app.WorkspaceStatus`（Phase 8）是汇总数据源，TUI 与 `status --root` 共用
- `app.ConflictPaths`、`app.Push(PushOptions{Resolve})`、`app.Restore(RestoreOptions{Force})`
- `cli.Env` 提供 Stdin/Stdout，测试可注入 `strings.Reader`/`bytes.Buffer`

### Established Patterns
- 命令解析用 `parseFlags`；退出码闭集；错误带 hint
- TUI 状态机做成可注入 reader/writer 的普通结构体，测试不需要伪终端

### Integration Points
- `internal/cli/cli.go` dispatch + 根 help + completion 表

</code_context>

<specifics>
## Specific Ideas

- 真终端验证：用 `script -qec` 分配 pty 并喂按键，确认渲染与按键响应（已在实现中执行）

</specifics>

<deferred>
## Deferred Ideas

- 内置 diff 渲染（用 `$EDITOR` 或 `diff` 命令）
- 目录级浏览、多选批量 resolve
</deferred>
