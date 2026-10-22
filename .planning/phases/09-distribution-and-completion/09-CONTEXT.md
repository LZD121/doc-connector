# Phase 9: 分发与补全 - Context

**Gathered:** 2026-10-23
**Status:** Ready for planning
**Mode:** Autonomous smart-discuss — recommended answers accepted without a live round-trip.

<domain>
## Phase Boundary

安装与使用不再依赖手工解压与记忆标志：package-manager 清单 + shell 补全。不改同步语义，不新增运行时依赖。**不替用户选许可证**——这是 winget 的唯一阻塞。

</domain>

<decisions>
## Implementation Decisions

### Homebrew cask
- 已存在 `homebrew_casks`（tap `LZD121/homebrew-tap`、`skip_upload` 按 `HOMEBREW_TAP_GITHUB_TOKEN` 求值）；本阶段只验证快照产物与 `goreleaser check`，不做上传

### Scoop
- `scoops:` 指向 `LZD121/scoop-bucket`，`skip_upload` 按 `SCOOP_BUCKET_GITHUB_TOKEN` 求值；缺 token 时只写 `dist/scoop/bucket/doc-connector.json`
- bucket 仓库本身由用户创建（工具不代建远端仓库，与 Out of Scope 一致）

### winget — 暂缓
- 仓库没有 LICENSE 文件，winget 清单要求 `license` 字段。选许可证（MIT/Apache/专有）是所有者决定，工具不代选
- 已记录为 DIST-04，附重启条件

### 补全
- 单一表格 `completionTable` 驱动 bash/zsh/fish 三份脚本，避免「bash 有、zsh 忘」
- `doc-connector completion <shell>`；未知 shell 退出 2 并列出支持项
- 脚本手写常量，不引入补全框架

### Claude's Discretion
- 脚本细节（是否补全路径参数、fish 的描述文案）

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `.goreleaser.yaml` 已有 builds/archives/checksums/changelog/homebrew_casks；`snapshot:` 已配置
- Phase 6 的 `Envelope` 与本阶段的 completion 无关（completion 是纯文本输出）
- `parseFlags`/`noPositional`/`Errorf(...).WithHint(...)`

### Established Patterns
- help 常量按命令分文件（drift：`diffUsage`/`syncUsage` 已在各自文件）
- 退出码闭集；每个错误带 hint

### Integration Points
- `internal/cli/cli.go` dispatch 增加 `completion`
- 根 help 的 Commands 列表

</code_context>

<specifics>
## Specific Ideas

- 补全脚本必须包含 `--json`/`--root`/`--follow-symlinks` 这些 v2 新标志，否则装了也没用

</specifics>

<deferred>
## Deferred Ideas

- winget（DIST-04，待许可证）
- 动态补全（调用二进制询问补全候选）：静态脚本足够
</deferred>
