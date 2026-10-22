---
phase: 06-observability-and-diagnostics
verified: 2026-10-22T15:43:16Z
status: passed
score: 5/5 success criteria verified
covered_digest: v3:sha256:ac00c8b427d4e9ca466cebbdd6d439b9d05c727935ece91168d7a51936b15cf3
covered_files:
  - .planning/phases/06-observability-and-diagnostics/06-01-PLAN.md
  - .planning/phases/06-observability-and-diagnostics/06-01-SUMMARY.md
  - .planning/phases/06-observability-and-diagnostics/06-02-PLAN.md
  - .planning/phases/06-observability-and-diagnostics/06-02-SUMMARY.md
  - .planning/phases/06-observability-and-diagnostics/06-03-PLAN.md
  - .planning/phases/06-observability-and-diagnostics/06-03-SUMMARY.md
  - .planning/phases/06-observability-and-diagnostics/06-04-PLAN.md
  - .planning/phases/06-observability-and-diagnostics/06-04-SUMMARY.md
  - .planning/phases/06-observability-and-diagnostics/06-05-PLAN.md
  - .planning/phases/06-observability-and-diagnostics/06-05-SUMMARY.md
  - internal/app/context.go
  - internal/app/doctor.go
  - internal/app/status.go
  - internal/cli/doctor.go
  - internal/cli/doctor_test.go
  - internal/cli/hints_test.go
  - internal/cli/json_test.go
  - internal/cli/output.go
  - internal/state/lock.go
behavior_unverified: 0
---

# Phase 6: 可观测性与诊断 Verification Report

**Phase Goal:** 机器可读的结果契约 + 一次性自我诊断，让脚本与用户都不再依赖人读文本。
**Verified:** 2026-10-22
**Status:** passed

| # | Success Criterion | Status | Evidence |
|---|-------------------|--------|----------|
| 1 | `status --json`、`push --json`、`pull --json` 各自输出**单个** JSON 对象，含 `schema_version`/`command`/`ok` | ✓ VERIFIED | `TestStatusJSONDocument`、`TestPushAndPullJSONDocument`（`decodeOne` 断言 schema_version=1、command、ok，并拒绝第二个文档）；`TestHumanOutputUnchangedWithoutJSON` 冻结人类输出 |
| 2 | 失败时 stdout 仍是单个 JSON 对象（`error.code`/`message`/`hint`），退出码同闭集，stdout 无 ANSI | ✓ VERIFIED | `TestJSONFailureIsSingleDocument`（仓库外 `status --json`：单个文档、ok=false、code=usage、message 非空）；`TestEveryErrorCarriesHint` 断言 hint 非空；`decodeOne` 拒绝 `\x1b[` |
| 3 | `doctor` 干净仓库全绿；篡改 exclude 块能指出具体问题与来源文件 | ✓ VERIFIED | `TestDoctorCleanProject`（无 fail、含 `[ok] git-version` 与 summary）；`TestDoctorReportsTamperedExclude`（exit 3、`[fail] exclude`、detail 含 exclude 绝对路径） |
| 4 | `doctor` 能报出锁持有者 PID/命令与遗留产物路径 | ✓ VERIFIED | `internal/app/doctor_test.go::TestDoctorLiveLock`（warn 含真实 pid 与 `push`）、`TestDoctorArtifacts`（warn 含 tmp 路径）；`state.Probe` 只读（`TestProbe` 断言持有者可继续 Release） |
| 5 | 每个错误码路径的 hint 都有测试断言 | ✓ VERIFIED | `TestEveryErrorCarriesHint`（未初始化 / 未知 revision / `--resolve` 未知路径）+ 源扫描 `errf` 27 处、缺 hint 0 处 |

**Score:** 5/5

## Requirements Coverage

OBS-01、OBS-02、OBS-03 全部满足。`--json` 当前覆盖 status/push/pull/doctor；`log`/`restore`/`init` 的 JSON 留待复用同一信封（不在本阶段承诺范围）。

## Notes

- 设计修正：`doctor` 的 remote 检查最初把「本地镜像没有远端 tip」判为 fail 并置 `RemoteUnreachable`，导致对空远端的新仓库 `doctor` 退出 4。已改为 warn：doctor 不联网，无权声称远端不可达；只有镜像本身读不出来才算 remote 类失败。
- `state.lockInfo` 已被导出的 `state.Holder` 取代，`Acquire`/`lockedError`/`Probe` 共用一个结构。
- 人类输出零回归：`status`/`push`/`pull` 的渲染分支未改，只在前面加了 `--json` 分支。
- 全量验证：`go test ./...` 107 通过 / 13 包；`go vet ./...` 干净；`golangci-lint run` 无问题。
