# Phase 3: 状态与干净拉取 - Context

**Gathered:** 2026-10-22
**Status:** Ready for planning
**Mode:** Autonomous (`mode: yolo`)

<domain>
## Phase Boundary

交付只读的三方比较核心与第一次入境同步：本地扫描（blob OID）、基线/远端树读取、共享的 15 行分类器、`status` 报告，以及 `pull` 在护栏、journal 与原子写保护下应用新增/修改/删除。冲突产物与推送属于 Phase 4；本阶段遇到任何冲突类（C）时 pull 在写入前整体拒绝并退出 1。

</domain>

<decisions>
## Implementation Decisions

### 扫描与忽略
- 扫描只经 `os.Root.FS()`（`fs.WalkDir`），每个路径先过 `fsx.ValidateRel`。
- 内置忽略（basename 大小写不敏感）：`.DS_Store`、`._*`、`Thumbs.db`、`ehthumbs.db`、`desktop.ini`、`.localized`；目录：任意层级 `.git`、`.svn`、`.doc-connector-conflicts`；任意路径含 `.theirs.`。
- `config.ignore.extra` 作为额外 basename 规则（大小写不敏感）参与匹配；v1 不解析 `.docignore`。
- 被忽略的路径不进入 L 也不进入 R（拉取时不会把垃圾拉回）。

### 内容 OID 与模式
- 唯一判定依据是 git blob OID：`hash-object [--write] --no-filters --stdin-paths`，cwd=docsDir，路径按字典序；`--no-filters` 保证字节精确。
- 可执行位 → `100755`，否则 `100644`；比较包含 mode。
- 符号链接在扫描时跳过并警告（不进入 L）；远端树中的 mode `120000` 视作非法输入，整次操作退出 3。
- 文件大小上限：v1 本阶段 `> max_bytes` 直接拒绝（退出 1），`--allow-large` 在 Phase 5 补齐。

### 分类器
- `trees.Tree = map[string]Entry{Mode, OID}`；`Classify(B,L,R) []Action` 是纯函数，严格实现 15 行表；`Clobber(B,L,R)` 复用同一 eq。
- 汇总：本地有改动 → ahead；远端有改动 → behind；两者都有 → diverged；否则 clean。C 类计入 diverged。
- `status` 对 C 行输出 `C <path>`；`pull` 在存在任何 C 时**不写任何文件**，列出冲突并退出 1。

### status
- 流程：加载 config/state → 只读护栏 → 扫描本地 → 默认 fetch（`--offline` 跳过）→ B/R → 分类 → 输出。
- fetch 失败：用 `fetched.json` 缓存的 tip，标注 “remote not checked”，最终退出 4。
- `status` 永不写 `state.json`；成功 fetch 只更新 `fetched.json`。
- 输出：一行 `status: <clean|ahead|behind|diverged>`，随后每条 ` <标记> <path>`，末行 `ignored: N`，以及 `remote: <短OID> (fetched <age> ago)`。

### pull
- 顺序：恢复未完成 journal → 加载/护栏 → fetch → 扫描 → 分类 → 冲突闸门 → 预校验所有远端路径 → 暂存远端 blob → 写 journal → 逐条 apply → 写 state → 删 journal/tmp。
- 暂存：一次 `cat-file --batch` 流式写入 `tmp/op-<rand>/files/<rel>`；不整块进内存。
- 写入：在目标目录创建临时文件 → fsync → rename；失败保留临时文件并报错，绝不原地截断（GUARD-07）。
- 删除：仅对 `remote-delete`（基线存在、远端删除、本地未改）执行；先把目标 rename 到 `tmp/op-*/trash/`，成功后清理；自底向上删除空目录，不越出 docs 根。
- 基线：仅当全部动作成功才推进 `synced_commit = REMOTE`；任何失败保持旧基线并报告已应用/未应用数量。
- journal 恢复是前滚幂等：write 目标已存在且 OID 相同 → skip；delete 目标不存在 → skip；完成后写 state_next、删 journal、删 tmp。
- `--dry-run` 走同一分类与前校验，打印动作，不写文件/state/ref。

### Claude's Discretion
- 输出文案与路径排序；tmp 目录命名；并发扫描细节（v1 顺序执行）。

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `config.Load/Save`、`state.Load/Save/Journal`、`gitstore.Store`（EnsureBare/Fetch/RefTip/TreeOf）、`project.GuardAll`、`fsx.Root/ValidateRel`、`ignore`（本阶段新增）。

### Established Patterns
- 领域错误携带 `ExitCode()`；写操作先护栏后写入；配置文件最后落盘。

### Integration Points
- 新增 `internal/ignore`、`internal/scan`、`internal/trees`、`internal/app`；CLI 的 status/pull 从 stub 改为调用 app。

</code_context>

<specifics>
## Specific Ideas

- 分类器测试表覆盖 15 行 + 模式变化；status 测试用 `touch` 与内容替换证明 OID 判定。
- pull 测试注入 rename 失败（目标父路径为文件）验证临时文件保留与基线不推进。

</specifics>

<deferred>
## Deferred Ideas

- `.theirs` 产物、`--resolve`、adopt-local、大小写/NFC、50MB 警告 → Phase 4/5。

</deferred>
