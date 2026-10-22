---
phase: 09-distribution-and-completion
verified: 2026-10-23T01:40:00Z
status: passed
score: 4/4 success criteria verified (winget split out to DIST-04)
covered_digest: v3:sha256:e99c4db45b9de86bf3bf0c0b9239e0a2d0c3e3e4ae5329b2ca872284bd4d29e3
covered_files:
  - .planning/phases/09-distribution-and-completion/09-01-PLAN.md
  - .planning/phases/09-distribution-and-completion/09-01-SUMMARY.md
  - .planning/phases/09-distribution-and-completion/09-02-PLAN.md
  - .planning/phases/09-distribution-and-completion/09-02-SUMMARY.md
  - .planning/phases/09-distribution-and-completion/09-03-PLAN.md
  - .planning/phases/09-distribution-and-completion/09-03-SUMMARY.md
  - internal/cli/cli.go
  - internal/cli/completion.go
  - internal/cli/completion_test.go
  - internal/cli/help.go
  - .goreleaser.yaml
behavior_unverified: 0
---

# Phase 9: 分发与补全 Verification Report

**Phase Goal:** 安装与使用不再依赖手工解压与记忆标志。
**Verified:** 2026-10-23
**Status:** passed

| # | Success Criterion | Status | Evidence |
|---|-------------------|--------|----------|
| 1 | 打 tag 后 tap 出现 cask，`brew install --cask` 可装且版本一致 | ✓ VERIFIED（产物级） | `goreleaser check` 通过；`goreleaser release --snapshot --clean --skip=publish` 写出 `dist/homebrew/Casks/doc-connector.rb`，cask 名称/二进制/首页字段指向 `doc-connector`。真实 tap 上传仍需 `HOMEBREW_TAP_GITHUB_TOKEN`（所有者秘密，本阶段不代填） |
| 2 | 缺 token 时发布仍绿，cask 只生成到 `dist/` | ✓ VERIFIED | 快照运行在无 `HOMEBREW_TAP_GITHUB_TOKEN` / `SCOOP_BUCKET_GITHUB_TOKEN` 的环境下 `release succeeded`，六平台归档 + `checksums.txt` 齐全 |
| 3 | Scoop 清单可被 `scoop install` 使用 | ✓ VERIFIED（产物级） | 同一次快照写出 `dist/scoop/bucket/doc-connector.json`；bucket 仓库 `LZD121/scoop-bucket` 需用户创建（工具不代建远端，与 Out of Scope 一致） |
| 4 | `completion bash\|zsh\|fish` 输出可 source 的脚本，覆盖六个子命令与 v2 全部标志 | ✓ VERIFIED | `TestCompletionCoversEveryCommand`（逐 shell 逐命令逐标志断言，含 `--json`/`--root`/`--follow-symlinks`）、`TestCompletionScriptsAreSyntacticallyPlausible`、`TestCompletionRejectsUnknownShell`（退出 2 + hint）、`TestCompletionRequiresShell` |

**Score:** 4/4

## Requirements Coverage

DIST-02 满足。DIST-01 缩减为「Homebrew cask + Scoop」并已按实际交付改写；winget 拆为 DIST-04（Future），原因见下。

## Notes — winget 为何没有交付

仓库根目录**没有 LICENSE 文件**，而 winget 清单（以及 winget-pkgs 的评审）要求 `license` 字段。许可证（MIT / Apache-2.0 / 专有…）是所有者对外的法律决定，工具不代选，所以本阶段：
- 不写 `winget:` 段（写了就必须编一个许可证值）；
- 把 winget 记入 `REQUIREMENTS.md` 的 Future 段（DIST-04），并附重启条件：在仓库根添加 LICENSE 后补 `winget:` 段，用 `goreleaser check` + 快照产物验证。

这是本里程碑唯一一处「计划有、交付无」的项，且是显式的需求改写而非静默缩水。

## 其它

- 全量验证：`go test ./...` 145 通过 / 13 包；`go vet ./...` 干净；`golangci-lint run` 0 issues。
