---
phase: 02-guardrails-and-init
verified: 2026-10-22T00:00:00Z
status: passed
score: 5/5 success criteria verified
covered_files:
  - .planning/phases/02-guardrails-and-init/02-01-PLAN.md
  - .planning/phases/02-guardrails-and-init/02-02-PLAN.md
  - .planning/phases/02-guardrails-and-init/02-03-PLAN.md
  - .planning/phases/02-guardrails-and-init/02-04-PLAN.md
  - internal/project/exclude.go
  - internal/project/guard.go
  - internal/gitstore/store.go
  - internal/gitstore/remote.go
  - internal/cli/init.go
  - internal/fsx/root.go
behavior_unverified: 0
---

# Phase 2: 隔离护栏与 init Verification Report

**Phase Goal:** 让 `init` 端到端可用，并在任何写操作前拒绝被跟踪或未被本工具规则排除的文档目录。
**Verified:** 2026-10-22
**Status:** passed

## Goal Achievement

| # | Success Criterion | Status | Evidence |
|---|-------------------|--------|----------|
| 1 | `init --remote <url>` 输出 project_id/分支/文档目录/下一步；配置落在 `.git/doc-connector/config.yaml`；重复执行结果不变 | ✓ VERIFIED | `TestInitEndToEnd`：退出 0，stdout 含 project/remote/docs/base/next；config 含全部字段；第二次运行 config/state 字节相等且 stdout 相等；真实二进制冒烟同样通过 |
| 2 | docs 不出现在 `git status`；`.gitignore` 字节不变；`.git/info/exclude` 只有一段受管块且重复 init 不重复追加 | ✓ VERIFIED | `TestInitEndToEnd` 断言 `git status --porcelain` 不含 private-docs/config.yaml 且 `.gitignore` 不存在；`TestEnsureExclude*` 断言幂等与 `.gitignore` 哈希不变；冒烟中 exclude 仅一个块 |
| 3 | docs 被跟踪、或 ignore 来源不是 info/exclude 时退出 3 并打印来源/模式 | ✓ VERIFIED | `TestInitRefusesTrackedDocs`（exit 3，消息含 tracked 与路径）、`TestGuardIgnoredSource` / `TestInitRefusesForeignIgnoreSource`（exit 3，消息含 `.gitignore`）；来源检查按 `check-ignore -z` 的 4 字段解析 |
| 4 | 空远端可引导；已有 `<project_id>/` 时采用并提示 pull，不修改本地文件；网络/认证失败退出 4 | ✓ VERIFIED | `TestStoreFetchEmptyRemote`、`TestInitEndToEnd`（base none → push）；`TestInitAdoptsExistingPrefix`（stdout 含 pull 与 not modified，docs 未被创建；无本地内容时基线留空，随后 pull 成功下载）；`TestDetectBranch` 与 `TestInitUnreachableRemoteExitsFour`（exit 4） |
| 5 | 主仓库 index 哈希、HEAD、refs、hooks、已跟踪集合在 init 前后完全一致 | ✓ VERIFIED | `TestSnapshotStable`（exclude+store+fetch 前后五类状态相等）与 `TestInitEndToEnd` 中真实 init 前后 `TakeSnapshot` 全等 |

**Score:** 5/5

## Requirements Coverage

| Requirement | Status | Evidence |
|-------------|--------|----------|
| INIT-01 | ✓ SATISFIED | init 输出与下一步命令 |
| INIT-04 | ✓ SATISFIED | `--force` 路径写 `project.id_source: explicit`（此前为层测试，现在 CLI 落盘） |
| INIT-05 | ✓ SATISFIED | 受管块写入、幂等、不碰 `.gitignore` |
| INIT-06 | ✓ SATISFIED | ls-remote 分支探测、空远端、失败映射 4 |
| INIT-07 | ✓ SATISFIED | 前缀采用 + pull 提示，本地 docs 未创建/修改 |
| INIT-08 | ✓ SATISFIED | config 六字段 + 原子 0600；state 同步 |
| GUARD-01 | ✓ SATISFIED | `ls-files` 空校验，退出 3 |
| GUARD-02 | ✓ SATISFIED | `check-ignore` 来源必须等于 info/exclude |
| GUARD-03 | ✓ SATISFIED | Snapshot 五类状态前后比较 |
| GUARD-04 | ✓ SATISFIED | fsx.Root 拒绝符号链接根、穿越、Windows 非法字符 |

## Cross-platform Build

linux/amd64、darwin/arm64、windows/amd64 交叉构建通过。

## Notes

- 首次 init 不创建 docs 目录；`launch` 之后由用户创建或由 Phase 3 的 pull 创建。
- adoption 识别远端前缀并提示 pull；仅当本地已有文件时才把基线设为远端 tip。全新机器（docs 为空）保持基线为空，否则 pull 会把远端文件误判为“本地删除”而下载不到任何内容（Phase 3 发现并修正，`TestInitAdoptsExistingPrefix` 覆盖）。
