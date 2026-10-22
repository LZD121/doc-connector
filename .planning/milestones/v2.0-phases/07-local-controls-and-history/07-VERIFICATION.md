---
phase: 07-local-controls-and-history
verified: 2026-10-23T00:20:00Z
status: passed
score: 5/5 success criteria verified
covered_digest: v3:sha256:b497b835b362eab240ea62f3a3f92aed79be1569f58491a8f74d5482b20e8ad1
covered_files:
  - .planning/phases/07-local-controls-and-history/07-01-PLAN.md
  - .planning/phases/07-local-controls-and-history/07-01-SUMMARY.md
  - .planning/phases/07-local-controls-and-history/07-02-PLAN.md
  - .planning/phases/07-local-controls-and-history/07-02-SUMMARY.md
  - .planning/phases/07-local-controls-and-history/07-03-PLAN.md
  - .planning/phases/07-local-controls-and-history/07-03-SUMMARY.md
  - .planning/phases/07-local-controls-and-history/07-04-PLAN.md
  - .planning/phases/07-local-controls-and-history/07-04-SUMMARY.md
  - .planning/phases/07-local-controls-and-history/07-05-PLAN.md
  - .planning/phases/07-local-controls-and-history/07-05-SUMMARY.md
  - .planning/phases/07-local-controls-and-history/07-06-PLAN.md
  - .planning/phases/07-local-controls-and-history/07-06-SUMMARY.md
  - internal/app/app_test.go
  - internal/app/context.go
  - internal/app/diff.go
  - internal/app/diff_test.go
  - internal/app/journal.go
  - internal/app/log.go
  - internal/app/pull.go
  - internal/app/push.go
  - internal/app/restore.go
  - internal/app/status.go
  - internal/cli/cli.go
  - internal/cli/diff.go
  - internal/cli/diff_test.go
  - internal/cli/help.go
  - internal/cli/init.go
  - internal/cli/locals_test.go
  - internal/cli/multidocs_test.go
  - internal/cli/output.go
  - internal/cli/pull.go
  - internal/cli/push.go
  - internal/cli/status.go
  - internal/config/config.go
  - internal/config/config_test.go
  - internal/ignore/filter.go
  - internal/ignore/filter_test.go
  - internal/project/exclude.go
  - internal/project/guard.go
  - internal/scan/scan.go
  - internal/scan/scan_test.go
behavior_unverified: 0
---

# Phase 7: 本地控制与历史视图 Verification Report

**Phase Goal:** 用户可以按本地需要收窄同步面（忽略、链接），并把一个项目映射到多个文档目录；同时能查看历史版本差异。
**Verified:** 2026-10-23
**Status:** passed

| # | Success Criterion | Status | Evidence |
|---|-------------------|--------|----------|
| 1 | `.docignore` 的 basename 与目录前缀规则生效，文件自身与被忽略路径都不进 status/push | ✓ VERIFIED | `TestDocIgnoreEndToEnd`（status 隐藏、远端树中不存在、`.docignore` 不入远端）、`TestDocIgnoreRules`、`TestDocIgnoreUnsupportedSyntax` |
| 2 | 默认仍拒绝符号链接并报出全部违规项；显式开启后根外目标被拒、根内链接触发同步 | ✓ VERIFIED | `TestFollowSymlinksEndToEnd`、`TestScanFollowSymlinks`（OID 等于目标、mode 100644/100755、键为链接路径）、`TestScanFollowSymlinkRefusesUnsafeTargets`（绝对/越界/悬空/目录目标，code 3 且逐一列出） |
| 3 | 旧单目录 config 自动迁移，迁移后行为逐字节一致 | ✓ VERIFIED | `TestLoadMigratesSchemaOne`（schema 1 → 一个空前缀映射）、`TestSaveWritesSchemaTwo`；全量 `internal/cli` 44 个测试未改动仍绿 |
| 4 | 多目录项目下 push 只影响本项目各前缀，restore 能按任一目录定位 | ✓ VERIFIED | `TestMultiDocsSync`（远端恰为 `<pid>/a.md` 与 `<pid>/notes/b.md`；clone+init+pull 还原到两个目录；`restore --from <commit> notes/b.md` 只写 `notes/`）、`TestInitKeepsEveryMapping` |
| 5 | `diff <commit> [path]` 不触网输出差异，无 pager 与 `--json` 下都有确定输出 | ✓ VERIFIED | `TestDiffCommand`（unified diff、路径过滤、未知 revision 退 2 带 hint、远端 URL 指向不存在路径仍出 patch、`--json` 单文档）、`TestDiff`（A/M/D、空树回退、二进制与 64 KiB 截断、不推进 baseline） |

**Score:** 5/5

## Requirements Coverage

LOCL-01、LOCL-02、LOCL-03、LOCL-04 全部满足。07-06 的 `--from`/`--json` 并入 07-05 交付（同两文件的第二遍改动没有价值）。

## Notes

- **`diff` 的设计偏差（已记录在代码注释）**：`store.git` 是无 worktree 的裸镜像，`git diff <base>:<pid>`（树 vs 工作区）会 `fatal: this operation must be run in a work tree`。实现改为比较 `<base>:<pid>` 与远端跟踪 tip 的 `<pid>`，任一侧缺前缀时用空树替代；路径过滤使用裸子树路径（带 `pid/` 前缀匹配不到，已实测）。
- **多目录的边界规则**：空前缀映射按定义拥有所有项目路径（这是 v1.0 布局的来源），因此「无主远端路径」守卫只在全前缀配置下触发；`TestMultiDocsSync` 用 `docs`/`notes` 全前缀配置覆盖该分支。
- 修复了 `init` 在手工改成多目录配置后重跑会丢掉其他 exclude 条目的缺陷（`TestInitKeepsEveryMapping`）。
- 全量验证：`go test ./...` 124 通过 / 13 包；`go vet ./...` 干净；`golangci-lint run` 无问题。
