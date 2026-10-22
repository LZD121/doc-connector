# Phase 5: 历史、硬化与发布 - Context

**Gathered:** 2026-10-22
**Status:** Ready for planning
**Mode:** Autonomous (`mode: yolo`)

<domain>
## Phase Boundary

补齐离线历史（`log`）与恢复（`restore`）、路径与体积硬化（大小写/NFC、Windows 保留名与长度、模式归一化、CRLF 往返）、以及三平台 CI 与 GoReleaser 发布配置。完成后 v1.0 可分发、可离线审计。

</domain>

<decisions>
## Implementation Decisions

### log
- 只读本地镜像，绝不 fetch。起点 `refs/remotes/origin/<branch>`；缺失 → 退出 2 并提示先 init/status。
- 格式：`%H%x00%aI%x00%an%x00%(trailers:key=Doc-Connector-Machine,valueonly)%x00%s`，NUL 分字段；默认 `--limit 20`。
- `--path <rel>` 在 `<pid>/<rel>` 内过滤；rel 先过 `ValidateRel`。
- 输出行：`<短OID>  <时间>  <machine>  <subject>`；末尾打印 fetched 缓存年龄（`log` 不更新缓存）。

### restore
- 用法：`restore <path>`（默认远端 tip）或 `restore <commit> <path>`；`--from` 覆盖默认。
- 目标路径在 rev 中不存在时，用 `log -1 <rev> -- <pid>/<path>` 找到最后一次提交再取 blob（恢复被删除文件）。
- 覆盖规则：目标存在且本地内容 != 基线（未同步改动）→ 拒绝，退出 1；`--force` 才覆盖。目标不存在 → 直接写。
- `--to <dir>` 写到用户目录（不经过 docs root），不改 unresolved。
- `--force` 同时删除对应 `.theirs.*`；若恢复内容 OID == 远端 OID，清除该 unresolved 记录。
- 恢复是普通本地变更，不自动 push、不推进基线。

### 路径与平台硬化
- 比较键 `NFC(lower(path))`；本地树与远端树各自检查键冲突，任一冲突 → 退出 3 并按组列出全部路径。
- 远端路径继续走 `ValidateSyncPath`（保留名、长度、Windows 非法字符）。
- Windows 上对 OID 相同但 mode 不同的条目做分类前归一化（chmod 差异不产生虚假修改）；非 Windows 保持 mode 参与比较。
- 字节精确：`--no-filters` 保证 CRLF 往返不变；测试覆盖 CRLF/二进制/UTF-16。

### 体积门
- 扫描与远端预检共用：`> max_bytes` → 拒绝（1）；`> warn_bytes` → 必须 `--force` 才继续，否则拒绝并给出提示。
- `--allow-large` 仍可显式放行 >max（用户明确选择）。
- `status` 只读：不做体积拒绝，仅报告。

### 发布
- `.github/workflows/ci.yml`：ubuntu/macos/windows × Go 1.26/1.27，`go vet`、`go test`、race（Linux/macOS）、golangci-lint（Linux）。
- `.goreleaser.yaml`（version: 2）：六平台静态构建、archives、checksums、changelog、homebrew_casks；`brews` 不使用。

</decisions>

<code_context>
## Existing Code Insights
- `store.Run`、`trees.ReadTree`、`app.Load`、`writeAtomicFromPath`、`state.Unresolved` 可直接复用。
- 新增 `internal/app/log.go`、`internal/app/restore.go`、`internal/fsx/casefold.go`。

</code_context>
