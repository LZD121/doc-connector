---
phase: 05-history-hardening-release
verified: 2026-10-22T00:00:00Z
status: passed
score: 5/5 success criteria verified
covered_files:
  - .planning/phases/05-history-hardening-release/05-01-PLAN.md
  - .planning/phases/05-history-hardening-release/05-02-PLAN.md
  - .planning/phases/05-history-hardening-release/05-03-PLAN.md
  - .planning/phases/05-history-hardening-release/05-04-PLAN.md
  - .planning/phases/05-history-hardening-release/05-05-PLAN.md
  - internal/app/log.go
  - internal/app/restore.go
  - internal/fsx/casefold.go
  - .github/workflows/ci.yml
  - .goreleaser.yaml
behavior_unverified: 0
---

# Phase 5: 历史、硬化与发布 Verification Report

**Phase Goal:** 离线 log/restore、路径与体积边界、三平台 CI 与发布流水线。
**Verified:** 2026-10-22
**Status:** passed

| # | Success Criterion | Status | Evidence |
|---|-------------------|--------|----------|
| 1 | 断网时 `log` 显示时间/机器/摘要；`--path` 过滤 | ✓ VERIFIED | `TestLogOffline`（把 remote 改成不存在后仍返回两条提交，`--path a.md` 只返回一条，显示 fetched 年龄） |
| 2 | `restore` 对未同步改动拒绝，`--force` 才覆盖；不自动提交；`--force` 删除 `.theirs.*` | ✓ VERIFIED | `TestRestoreHistory`（未同步 exit 1、force 成功、基线不变、删除文件恢复、--to）、`TestRestoreForceResolvesConflict`（副本删除、unresolved 清除） |
| 3 | 大小写或 NFC/NFD 冲突整次拒绝并列出全部路径；Windows 保留名与超长路径可行动拒绝 | ✓ VERIFIED | `TestCaseCollisionRefused`（exit 3、同时列出 a.md/A.md）、`TestCaseCollisions`（NFC/NFD）、`TestValidateSyncPathReserved`（CON/NUL/尾点/超长） |
| 4 | >100MB 拒绝；>50MB 警告并要求 `--force` | ✓ VERIFIED | `TestSizeGate`（缩放阈值 10/20 字节：15B 无 force 拒绝、force 通过；25B 即使 force 也拒绝；allow-large 通过）；`checkRemoteSize` 同一规则用于远端 |
| 5 | 三平台 CI 矩阵；GoReleaser 产出六平台归档与校验和 | ✓ VERIFIED（配置） | `.github/workflows/ci.yml`（3 OS × Go 1.26/1.27、vet/test/race/lint/tidy）、`.goreleaser.yaml`（linux/darwin/windows × amd64/arm64、checksums、cask）；本机六目标交叉构建与 `goreleaser check` / `--snapshot` 全部通过。Cask 暂存 `dist/`（`skip_upload: true`），待 tap 仓库与 token 就绪后改为 `auto`。GitHub Actions 首次矩阵执行结果见下 |

**Score:** 5/5

## Requirements Coverage

HIST-01、HIST-02、HIST-03、CONF-04、GUARD-05、GUARD-08、PLAT-02 已满足。PLAT-01/PLAT-03 的实现与 CI 配置已交付；首次矩阵运行发生在推送/打标签时。

## Notes

- `log` 的格式化必须用 0x1e 分隔记录：git trailer 值自带换行，按换行分帧会把一条提交拆成两行（已修正并有测试）。
- 行为修正：全新机器 init（docs 为空）不再把远端 tip 记为基线；否则 pull 会把远端文件当作本地删除而无法下载（Phase 2 的 adoption 语义已同步更新）。
- `restore` 允许 flags 出现在位置参数之后（内部重排），因此 `restore <commit> <path> --force` 与 `restore --force <path>` 都可用。
- 首次 CI 运行暴露了跨平台测试假设（git 身份、macOS `/var` 符号链接、Windows 模式位/保留名/POSIX 脚本）：已修正并以 os.SameFile 比较路径、按平台跳过不可表达行为。修复后 CI 全绿。
