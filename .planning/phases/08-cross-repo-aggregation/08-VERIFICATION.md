---
phase: 08-cross-repo-aggregation
verified: 2026-10-23T01:05:00Z
status: passed
score: 5/5 success criteria verified
covered_digest: v3:sha256:45ebad5fc1e64638dfed69dbda12e813c37fc14a8bb74f79535f81d260482734
covered_files:
  - .planning/phases/08-cross-repo-aggregation/08-01-PLAN.md
  - .planning/phases/08-cross-repo-aggregation/08-01-SUMMARY.md
  - .planning/phases/08-cross-repo-aggregation/08-02-PLAN.md
  - .planning/phases/08-cross-repo-aggregation/08-02-SUMMARY.md
  - .planning/phases/08-cross-repo-aggregation/08-03-PLAN.md
  - .planning/phases/08-cross-repo-aggregation/08-03-SUMMARY.md
  - .planning/phases/08-cross-repo-aggregation/08-04-PLAN.md
  - .planning/phases/08-cross-repo-aggregation/08-04-SUMMARY.md
  - internal/app/lock_guard_test.go
  - internal/app/pull.go
  - internal/app/push.go
  - internal/app/workspace.go
  - internal/app/workspace_test.go
  - internal/cli/cli.go
  - internal/cli/help.go
  - internal/cli/status.go
  - internal/cli/sync_test.go
  - internal/cli/workspace.go
  - internal/cli/workspace_test.go
behavior_unverified: 0
---

# Phase 8: 跨仓库聚合 Verification Report

**Phase Goal:** 一次看到并驱动多个仓库，同时不引入全局状态、不自动解决冲突。
**Verified:** 2026-10-23
**Status:** passed

| # | Success Criterion | Status | Evidence |
|---|-------------------|--------|----------|
| 1 | `status --root` 一屏列出深度 ≤2 的全部已初始化仓库及 state/ahead/behind/冲突数；无 config 的仓库只计入跳过计数 | ✓ VERIFIED | `TestStatusRootAggregates`、`TestStatusRootSkipsUninitializedAndDepth`（深度 3 不可见）、`TestDiscover`（`.git` 无 config → skipped） |
| 2 | 存在 diverged 仓库时 `sync --root` 标 blocked 并继续，绝不写副本、绝不 resolve，退出码 1 | ✓ VERIFIED | `TestSyncRootVerdicts`：clean/behind/ahead/diverged 四仓一次跑完，blocked 仓无 `.theirs.`、无新提交、本地字节未变，退出码 1 |
| 3 | 单仓失败被记录为 failed 且不影响其余仓库，退出码 4；被持锁的仓库报 locked，与 failed 分列 | ✓ VERIFIED | `TestStatusRootExitCodeIsWorst`（2 vs 4 → 4）、`TestDiscoverVerdicts`（`ErrLocked` → locked）、`TestPushRefusesWhileLocked` / `TestPullRefusesWhileLocked`（真实持锁，exit 3，无提交/无 journal） |
| 4 | `sync --root --dry-run` 不写任何文件、不推进 base | ✓ VERIFIED | `TestSyncDryRunWritesNothing`（`state.json` 字节相同、远端无 `refs/heads/main`） |
| 5 | 批量按顺序执行且每仓重新 fetch（测试断言无并发） | ✓ VERIFIED（行为级） | `TestSyncSharedRemote`：两仓共享一个私有远端，一次批量双双落库——并发推送会在 lease 上竞争到重试耗尽；实现为单循环串行调用 `Status`（内含 fetch）再 `Pull`/`Push` |

**Score:** 5/5

## Requirements Coverage

WORK-01、WORK-02 满足。同时补上 v1.0 遗留的写锁缺口（`push`/`pull` 现在取项目锁）。

## Notes

- **对 STACK 设计的有意收窄**：设计文档要求「每个写命令与 `status` 都取排他锁」，实现只让 `push`/`pull`（以及既有的 `init`）取锁；`status`/`log`/`restore`/`diff` 只读或只写镜像缓存，不取锁。理由：`status --root` 要在其他仓库正在同步时仍然可用，而写路径的互斥才是丢改动的来源。
- **`locked` 与 `failed` 的区分**依赖 `isLockError`：先 `errors.Is(state.ErrLocked)`，再回退到「码 3 且消息含 lock」——因为 `Acquire` 返回的 `lockedError` 不是那个哨兵。
- 串行性没有用并发检测器断言（会引入测试钩子）；改为用「共享私有远端的双仓批量」证明行为，这是能观察到的真实约束。
- 全量验证：`go test ./...` 141 通过 / 13 包；`go vet ./...` 干净；`golangci-lint run` 无问题。
