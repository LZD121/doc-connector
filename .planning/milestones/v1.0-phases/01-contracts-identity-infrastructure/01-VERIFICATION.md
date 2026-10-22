---
phase: 01-contracts-identity-infrastructure
verified: 2026-10-22T00:00:00Z
status: passed
score: 5/5 success criteria verified
covered_files:
  - .planning/phases/01-contracts-identity-infrastructure/01-01-PLAN.md
  - .planning/phases/01-contracts-identity-infrastructure/01-02-PLAN.md
  - .planning/phases/01-contracts-identity-infrastructure/01-03-PLAN.md
  - .planning/phases/01-contracts-identity-infrastructure/01-04-PLAN.md
  - cmd/doc-connector/main.go
  - internal/cli/cli.go
  - internal/gitstore/exec.go
  - internal/project/identity.go
  - internal/config/config.go
  - internal/state/lock.go
behavior_unverified: 0
---

# Phase 1: 契约、身份与基础设施 Verification Report

**Phase Goal:** 交付可构建的单二进制骨架：六个子命令、稳定的退出码与输出契约、确定性 project_id、隐藏配置与原子状态、跨进程锁。
**Verified:** 2026-10-22
**Status:** passed

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `--version` 与任一子命令 `--help` 无需仓库即退出 0；未实现子命令退出 2 并说明 | ✓ VERIFIED | `TestRootSurface`、`TestCommandHelpAndStubs`；二进制冒烟：`/tmp/dc --version`→0，`/tmp/dc status`→2 且 stderr 含 `not implemented yet` |
| 2 | 同一主仓库任意路径/机器推导相同 12 位 id；浅克隆/无根/多根退出 2 并列出原因 | ✓ VERIFIED | `TestDeriveID`（含固定向量）、`TestDeriveRejectsUnbornAndShallow`、`TestDeriveRejectsMultipleRoots`（消息含两个根 OID）、`TestDiscover`（子目录定位） |
| 3 | `init --project-id` 不一致且无 `--force` 退出 2；`--force` 时 `id_source: explicit` | ✓ VERIFIED | `TestResolveExplicitMismatch`、`TestInitRejectsMismatchedExplicitID` 覆盖 CLI 拒绝与 force 接受；`config.Project.IDSource` 的持久化经 `config.Save`/`Load` 往返测试覆盖。CLI 端配置落盘按阶段边界在 Phase 2 随完整 init 一并交付（Phase 1 不写半成品配置） |
| 4 | 全部 git 调用参数切片；git < 2.30 动作前失败；stdout/stderr 分离且不发起交互提问 | ✓ VERIFIED | `TestRunnerUsesArgvWithoutShell`（含空格与 `$` 的实参逐字比较）、`TestRunnerErrorCarriesStderrAndExitCode`；`cleanEnv` 断言 `GIT_TERMINAL_PROMPT=0`；假 git 2.29 冒烟→退出 2 且消息含 `requires git >= 2.30` |
| 5 | 同一 project 两个写操作并发，后者有限等待后退出 3 | ✓ VERIFIED | `TestLockContention`（包层）与 `TestInitLockContentionExitsThree`（CLI 层，init 持锁路径）均断言退出 3 并打印持有者；首个写命令在 Phase 2 完成后可正常完成 |

**Score:** 5/5

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `cmd/doc-connector/main.go` | 仅组装版本、信号与 cli.Run | ✓ EXISTS + SUBSTANTIVE | 无业务打印、无 shell 调用 |
| `internal/cli` | 六命令、帮助、退出码映射、错误输出 | ✓ EXISTS + SUBSTANTIVE | 无 stub 遗留（五个业务命令有意拒绝） |
| `internal/gitstore` | 唯一 git 缝隙、env 清洗、版本门、分类 | ✓ EXISTS + SUBSTANTIVE | 不存在 `exec.Command(` 与 `sh -c` |
| `internal/project` | 定位、推导、显式 id、歧义拒绝 | ✓ EXISTS + SUBSTANTIVE | 无“取最小根”等猜测路径 |
| `internal/config` | YAML、未知键保留、校验、0600 原子写 | ✓ EXISTS + SUBSTANTIVE | 未知键往返测试通过 |
| `internal/state` | state/fetched/journal、原子写、flock | ✓ EXISTS + SUBSTANTIVE | 失败注入后无临时文件残留 |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| main | cli.Run | 直接调用 | ✓ WIRED | 退出码成为进程状态 |
| cli init | project.Resolve | gitstore | ✓ WIRED | 四类身份场景经 CLI 测试 |
| cli init | state.Acquire | flock | ✓ WIRED | 竞争退出 3 |
| project | gitstore.Runner | rev-parse / rev-list | ✓ WIRED | 无其它 git 执行点 |

## Requirements Coverage

| Requirement | Status | Evidence |
|-------------|--------|----------|
| CLI-01 | ✓ SATISFIED | 六命令 `--help` + 根 `--version` 测试 |
| CLI-02 | ✓ SATISFIED | `exit.go` 闭集；各场景退出码测试 |
| CLI-03 | ✓ SATISFIED | 测试断言 stdout 无诊断；`GIT_TERMINAL_PROMPT=0` |
| CLI-04 | ✓ SATISFIED | 错误含原因/退出码语义/hint；无未经验证的修复建议 |
| INIT-02 | ✓ SATISFIED | 固定向量 + 跨克隆一致性（同一工作树多路径） |
| INIT-03 | ✓ SATISFIED | shallow/unborn/multi-root 全拒绝并给出原因 |
| INIT-04 | ✓ SATISFIED (logic) | 校验与 id_source 判定完成；CLI 持久化随 Phase 2 init 落地 |
| GUARD-06 | ✓ SATISFIED | flock 锁 + 有限等待 + 退出 3 |
| PLAT-01 | ✓ SATISFIED | 参数切片；`GOOS=windows,darwin` 交叉构建见下 |

## Cross-platform Build

```
GOOS=linux   GOARCH=amd64 go build ./...   ok
GOOS=darwin  GOARCH=arm64 go build ./...   ok
GOOS=windows GOARCH=amd64 go build ./...   ok
```

## Scope Notes

- `init` 在 Phase 1 只做身份解析与锁；远端探测、exclude、裸镜像与配置落盘属于 Phase 2，避免“配置存在但文档未隐藏”的危险中间态。
- 锁目录 `<git>/.git/doc-connector/` 会因加锁而创建；不写 `config.yaml` 或 `state.json`。
