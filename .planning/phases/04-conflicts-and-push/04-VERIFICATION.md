---
phase: 04-conflicts-and-push
verified: 2026-10-22T00:00:00Z
status: passed
score: 5/5 success criteria verified
covered_files:
  - .planning/phases/04-conflicts-and-push/04-01-PLAN.md
  - .planning/phases/04-conflicts-and-push/04-02-PLAN.md
  - .planning/phases/04-conflicts-and-push/04-03-PLAN.md
  - .planning/phases/04-conflicts-and-push/04-04-PLAN.md
  - .planning/phases/04-conflicts-and-push/04-05-PLAN.md
  - internal/conflict/name.go
  - internal/gitstore/plumbing.go
  - internal/app/push.go
  - internal/app/pull.go
behavior_unverified: 0
---

# Phase 4: 冲突与推送 Verification Report

**Phase Goal:** 本地优先的冲突产物与显式解决；租约推送、子树拼接、重试与基线推进。
**Verified:** 2026-10-22
**Status:** passed

| # | Success Criterion | Status | Evidence |
|---|-------------------|--------|----------|
| 1 | 同路径双方修改保留本地并生成 `<name>.theirs.<machine>`（`.2`/`.3`），列出冲突并退出 1；首次 pull 同名不同内容同样保留本地 | ✓ VERIFIED | `TestPushResolve`（both-modified：本地字节不变、副本内容为远端、exit 1）、`TestPullConflictShapes`（add/add、delete/modify、modify/delete、第二次 pull 不新增副本）、`TestArtifactPathDedupe`（`.2`/`.3`） |
| 2 | `.theirs.*` 不上远端、不被 status/push 当普通变更；未解决时 push 退出 1，`--resolve` 后恢复并删除产物 | ✓ VERIFIED | `TestFilterMatch`（`*.theirs.*` 被忽略）、`TestPushResolve`（status 输出不含 `.theirs.`；push 前拒绝；resolve 后远端为本地内容且产物消失） |
| 3 | 两个项目推同一远端，各自 `<pid>/` 与根级文件共存；提交含三个 trailers | ✓ VERIFIED | `TestTwoProjectsSameRemote`（两个 pid 前缀均存在）、`TestPushEndToEnd`（trailers 与 other-project 根条目保留） |
| 4 | 本地无变化 → 无空提交且退出 0；非快进自动 fetch/重建/重试（≤2），同前缀竞争拒绝并提示 pull；仅成功后推进基线 | ✓ VERIFIED（retry 窗口见 Notes） | `TestPushEndToEnd`（no-op）、`TestPushClobberAfterOtherMachine`（exit 1、提示 pull、基线不变）、`TestPushLeaseRejectionIsRace`（lease 拒绝被分类为 race）、重试循环实现见 `internal/app/push.go` |
| 5 | `push --dry-run` 与 `pull --dry-run` 打印动作，工作区/state/远端 ref 字节不变 | ✓ VERIFIED | `TestPushDryRun`（无 ref、无 state 变化）、`TestPullDryRun`（无文件、无 state） |

**Score:** 5/5

## Requirements Coverage

SYNC-05、SYNC-06、PULL-03、CONF-01、CONF-02、CONF-03、CONF-05、PUSH-01..06 全部满足。

## Notes

- 并发重试循环（重新 fetch → 重算 clobber → 新 tip 重建 → 退避）已实现并有 lease 拒绝分类测试；真实两个进程在同一毫秒窗口内的竞争未在测试中复现（需要注入网络延迟）。
- `--resolve` 只接受当前 unresolved 路径；未知路径退出 2。
- 有冲突时 pull 仍应用非冲突改动，但基线不推进，避免“未解决冲突被当作已同步”。
