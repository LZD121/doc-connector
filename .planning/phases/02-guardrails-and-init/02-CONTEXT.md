# Phase 2: 隔离护栏与 init - Context

**Gathered:** 2026-10-22
**Status:** Ready for planning
**Mode:** Autonomous (`mode: yolo`, recommended answers accepted)

<domain>
## Phase Boundary

让 `init` 端到端可用：探测远端默认分支、创建/更新裸镜像、把文档目录写进 `.git/info/exclude` 的受管块，并在任何写操作前拒绝已被跟踪或未被本工具规则排除的文档目录。完成后用户能在主仓库执行 `init --remote <url>`，随后 `git status` 不出现文档目录，且第二次执行 init 不改变任何字节。

本阶段不实现 status/pull/push；`init` 的 adopton 判定只做“远端 `<project_id>/` 是否存在”，不扫描本地内容（扫描在 Phase 3）。

</domain>

<decisions>
## Implementation Decisions

### exclude 受管块
- 块内容固定为三行（行尾 `\n`）：
  ```
  # doc-connector:begin
  /<docs_dir>/
  # doc-connector:end
  ```
  `docs_dir` 转为 slash 分隔；前导 `/` 锚定到工作树根；空格用反斜杠转义。`#` 不会出现在首位。
- 文件不存在 → 创建；存在且已含同内容块 → 字节不变（幂等）；存在不同内容的受管块 → 只替换该块，其余字节保持不变；无块 → 追加，若原文件不以 `\n` 结尾先补一个换行。
- 只写 `<common-dir>/info/exclude`；**永不**读写 `.gitignore`。写入用原子替换并保留原文件权限（默认 0644）。
- 解析既有块用行扫描 + 标记，不引入正则库。

### 隔离护栏
- 顺序不可交换：先 `ls-files -z -- <docs_dir>`（必须为空），再 `check-ignore -v -z --stdin`（必须命中且来源 == 绝对化后的 `.git/info/exclude`）。
- `check-ignore -v -z --stdin` 的输出是 4 个 NUL 字段一组：`source\0linenum\0pattern\0pathname\0`。按组解析，`source` 绝对化后与 exclude 路径比较；不按冒号切分（Windows 盘符含冒号）。
- 任一失败 → 退出 3，消息列出具体检查项、来源文件与模式；不回退、不忽略。
- `Snapshot` 记录 index 字节哈希、HEAD、全部 refs、hooks 目录中每个文件名与内容哈希、`ls-files -z` 集合哈希；仅用于集成测试断言 init 前后不变（运行时不做断言，避免把并发用户的正常操作误判为违规）。
- `docs_dir` 允许尚不存在（首次 init 不创建目录）；若存在且为符号链接 → 退出 3。

### init 流程
1. 版本门 → 仓库定位（linked worktree / bare 拒绝）→ 身份解析。
2. `--remote` 必填；内嵌凭据拒绝；`--branch` 用 `git check-ref-format --branch` 校验。
3. 写前子集护栏：docs 未被跟踪（退出 3）。
4. 取锁（10s，退出 3）。
5. `ls-remote --symref --exit-code <url> HEAD`：有输出取 `ref: refs/heads/<X>`；exit 2 → 空远端，branch = `--branch` 或 `main`；其它失败 → 退出 4。
6. 创建 `<git-dir>/doc-connector/store.git`（仅当不存在）`git init --bare -b <branch>`；`fetch --no-tags --prune <url> '+refs/heads/*:refs/remotes/origin/*'`（空远端也成功）。
7. `REMOTE = rev-parse --verify -q refs/remotes/origin/<branch>`；`ls-tree <REMOTE> <pid>` 判断前缀。
8. 写 exclude 块；跑完整护栏（未跟踪 + 来源）。
9. adoption：前缀存在 → `synced_commit = REMOTE`、`baseline_tree = rev-parse REMOTE:<pid>`，输出“先 pull”；前缀不存在 → base 为空，输出“可 push”。
10. 配置与状态最后写（原子、0600），任何一步失败都不写配置。

### 幂等与重入
- 已有配置且 `project.id` 与本次解析一致：复用已有 machine/docs_dir/remote/branch，除非对应 flag 显式给出；重写配置内容保持语义不变；exclude 块幂等；state 不重置。
- 已有配置且 `project.id` 不同：退出 2，提示这是另一个项目（`--force` 仅用于显式 ID 不匹配的场景，不用于换项目）。
- 第二次 init 必须满足：主仓库 index/HEAD/refs/hooks/已跟踪集合字节不变；`git status` 仍不显示 docs。

### Claude's Discretion
- 输出格式的具体措辞；store 创建失败时的清理（保留半成品 store 可被下次 init 复用，不删除用户数据）。

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/cli/init.go` 已解析全部 flag 并完成身份解析；Phase 2 在其后追加 bootstrap。
- `internal/gitstore` 提供 `Runner.Run/Cmd`、版本门与错误分类；新增 store 辅助函数。
- `internal/state` 提供原子写、Journal 与 flock；`internal/config` 提供 schema/校验/原子保存。

### Established Patterns
- 所有写操作先护栏后写入；配置最后落盘。
- 错误通过带 `ExitCode()` 的领域错误向上传递。

### Integration Points
- `internal/project` 增加 `exclude.go`、`guard.go`；`internal/gitstore` 增加 `store.go`、`remote.go`；新增 `internal/fsx`（os.Root 包装）。

</code_context>

<specifics>
## Specific Ideas

- 测试远端一律用 `file://` 裸仓库 + `t.TempDir()`，并隔离 HOME/GIT_CONFIG_*。
- 认证失败用不存在的 `file://` 路径模拟，必须映射退出 4。

</specifics>

<deferred>
## Deferred Ideas

- 本地/远端内容差异的 ambiguity 判定与 `--adopt-local` → Phase 3/4 在扫描与分类器就绪后实现。
- `.docignore` → v2。

</deferred>
