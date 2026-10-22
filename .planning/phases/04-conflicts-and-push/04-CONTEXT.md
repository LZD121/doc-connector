# Phase 4: 冲突与推送 - Context

**Gathered:** 2026-10-22
**Status:** Ready for planning
**Mode:** Autonomous (`mode: yolo`)

<domain>
## Phase Boundary

完成写路径：pull 的冲突产物与 unresolved 集合、`push` 的子树构建/租约推送/竞争重试/基线推进、`push --resolve`、以及 push/pull 的 `--dry-run`。不包含 restore（Phase 5）。

</domain>

<decisions>
## Implementation Decisions

### 冲突产物
- 命名 `<path>.theirs.<machine>`；已存在则 `.2`、`.3` …（上限 99）；超长时落到 `.doc-connector-conflicts/<sha1(path)[:8]>-<base>.theirs.<machine>`。
- 已在本路径 unresolved 中 → 不再生成新副本（避免每次 pull 产生 `.4`）。
- add/add 与 both-modified：保留本地，写副本；delete/modify（本地删、远端改）：副本写在原父目录，本地保持删除；modify/delete（本地改、远端删）：无副本，仅 unresolved。
- 产物永不同步（扫描忽略 `*.theirs.*` 与 `.doc-connector-conflicts/`）。
- 有冲突时仍应用非冲突的远端增删改；基线**不推进**；退出 1。

### push
- 护栏 → 恢复 journal → unresolved 闸门（未通过 `--resolve` 的条目 → 退出 1）→ fetch → B/L/R → 历史改写检测 → clobber 闸门 → 建树 → commit → lease push（最多 3 次尝试）。
- `--resolve <path>` 只接受当前 unresolved 内的路径（否则退出 2）；成功推送后删除对应产物并清除记录。
- clobber = `!eq(R,B) && !eq(L,R)`（含远端删除）；`adoption: local` 首推跳过。
- 子树：`read-tree --empty` + `update-index -z --index-info` + `write-tree`（临时 `GIT_INDEX_FILE`，绝不碰主仓库 index）。
- 根树：`ls-tree -z <tip>` 过滤 `<pid>` → 追加 `040000 tree <Tsub>` → `mktree -z`；其它项目条目字节原样保留。
- 无变化（Troot == tree(tip)）→ 不建提交、不推送、退出 0。
- 提交消息含 `Doc-Connector-Project/Machine/Files` trailer；合成身份或 config.author。
- 推送使用 `--force-with-lease=refs/heads/<branch>:<expect>`；竞争 → 重新 fetch、重算 clobber、在最新 tip 上重建、100/300/900ms 退避；重试前重扫，本地变化则中止。
- 成功后才推进基线；失败保留 `refs/doc-connector/pending`。
- `--dry-run`：扫描不写 blob、`write-tree --missing-ok`、`mktree --missing`，不建提交/ref/push/state。

### 退出码
- unresolved 未解决、clobber、历史改写 → 1；非法 `--resolve` → 2；网络 → 4；路径/大小违规 → 3/1。

</decisions>

<code_context>
## Existing Code Insights
- 复用 `trees.Classify/Clobber`、`scan.Scan`、`fsx`、journal 与 `state.Unresolved`。
- 新增 `internal/gitstore/plumbing.go`、`internal/conflict`、`internal/app/push.go`。

</code_context>
