---
phase: 03-status-and-pull
verified: 2026-10-22T00:00:00Z
status: passed
score: 5/5 success criteria verified
covered_files:
  - .planning/phases/03-status-and-pull/03-01-PLAN.md
  - .planning/phases/03-status-and-pull/03-02-PLAN.md
  - .planning/phases/03-status-and-pull/03-03-PLAN.md
  - .planning/phases/03-status-and-pull/03-04-PLAN.md
  - .planning/phases/03-status-and-pull/03-05-PLAN.md
  - internal/scan/scan.go
  - internal/trees/classify.go
  - internal/app/status.go
  - internal/app/pull.go
behavior_unverified: 0
---

# Phase 3: 状态与干净拉取 Verification Report

**Phase Goal:** 只读三方比较与 `status`；`pull` 在护栏、journal 与原子写保护下应用增删改。
**Verified:** 2026-10-22
**Status:** passed

| # | Success Criterion | Status | Evidence |
|---|-------------------|--------|----------|
| 1 | `status` 给出 clean/ahead/behind/diverged 与逐文件 unchanged/local-only/remote-only/both-changed/deleted 变体，并点名双方都改的路径 | ✓ VERIFIED | `TestStatusAndPullEndToEnd`（ahead/diverged、A/M 行）、`TestPullConflictRefuses`（`status: diverged` + `C a.md`）、`TestClassifyAllRows` 覆盖全部 15 行 |
| 2 | `touch` 或复制同内容不产生差异；同尺寸修改被报告 | ✓ VERIFIED | `TestScanContentIdentity`：touch 后 Entry 全等；同尺寸内容替换后 OID 变化；分类器只比较 Mode/OID |
| 3 | 远端不可达时仍报告本地与基线，明确标注“远端未检查”，退出 4；status 与 pull 共用分类 | ✓ VERIFIED | `TestStatusUnreachableRemote`（结果非空、RemoteChecked=false、exit 4）；`app.Status` 与 `app.Pull` 均调用 `trees.Classify`；grep 确认单一调用点 |
| 4 | `pull` 只改文档目录；删除仅对“基线存在、远端删除、本地未改”生效；本地存在但基线没有的文件永不删除；冲突时拒绝并退出 1 | ✓ VERIFIED | `TestStatusAndPullEndToEnd`（写入 1、删除 1、local.md 保留）、`TestPullConflictRefuses`（exit 1、本地字节不变、基线不推进）、`TestPullFailureKeepsBaseline`（失败不推进） |
| 5 | 远端 `..`/绝对路径/符号链接/保留名/超长路径使整次 pull 失败且不写文件；kill 后 journal 收敛；替换失败保留临时文件 | ✓ VERIFIED | `TestPullRejectsUnsafeRemotePaths`（NUL.txt → exit 3，未写入）、`TestReadTreeRejectsSymlink`（exit 3）、`TestJournalRecovery`（前滚幂等、清理 tmp）、`TestWriteAtomicFailureKeepsTemp`（临时文件保留） |

**Score:** 5/5

## Requirements Coverage

SYNC-01、SYNC-02、SYNC-03、SYNC-04、PULL-01、PULL-02、PULL-04、PULL-05、PULL-06、GUARD-07 全部满足。SYNC-06 的 pull 侧（同路径双方修改 → exit 1）已满足，push 侧闸门在 Phase 4 完成，故该需求保持进行中。

## Notes

- `status` 的 diverged 表示“两侧都有改动”，与 pull 的冲突闸门（同路径 C）区分；非冲突的双向改动可安全应用（本地文件从不被远端覆盖）。
- 远端被删除的分支（tip 为空但基线非空）视为历史改写，pull 退出 1，不删除本地文件。
