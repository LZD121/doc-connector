# Phase 1: 契约、身份与基础设施 - Context

**Gathered:** 2026-10-22
**Status:** Ready for planning
**Mode:** Autonomous (recommended answers accepted; `mode: yolo`)

<domain>
## Phase Boundary

交付可构建的单二进制骨架：六个子命令的 CLI 表面与退出码契约、唯一 git 执行缝隙（参数切片 + 环境清洗 + 版本前置门）、确定性 project_id 推导（含浅克隆/多根/无根拒绝）、隐藏 YAML 配置、原子 JSON 状态与跨进程锁。

本阶段**不**实现 push/pull/status/log/restore 的业务逻辑，也不完成 `init` 的远端探测、exclude 写入与裸镜像引导——那些属于 Phase 2。`init` 在本阶段只做身份解析与 `--project-id`/`--force` 校验，然后以退出码 2 明确拒绝，绝不写出一份半成品配置（半成品配置会让用户误以为文档已被 exclude，进而把私有文档提交进团队仓库）。

</domain>

<decisions>
## Implementation Decisions

### CLI 契约与退出码
- 退出码采用 `REQUIREMENTS.md` CLI-02 的闭集：`0` 成功/无变更，`1` 冲突或需人工介入，`2` 用法/身份/环境错误，`3` 隔离或锁失败，`4` 网络或认证失败，`130` 中断。
- `STACK.md` §8 与 `ARCHITECTURE.md` §Exit code mapping 中的 `5`（git 子进程失败/本地 I/O）**不采用**：需求文档是唯一契约。git 子进程失败按语义归类——网络/认证 → 4；其余 → 1（内部/I-O 失败）。本决策记录在代码常量注释中，避免后续阶段再次分叉。
- 根命令无参数 → 用法写 stderr，退出 2；`--version`、`--help` 写 stdout，退出 0，且不要求仓库、不运行 git 版本门。
- 已定义但未实现的子命令以一个稳定的错误消息退出 2（`"<cmd>: not implemented yet"`），便于 testscript 断言。
- stdout 只写命令结果；stderr 写诊断与错误。所有命令禁止交互式提问。

### Git 执行缝隙
- 所有 git 调用经 `internal/gitstore.Runner`，参数切片，禁止 shell 字符串；`exec.CommandContext` + 显式 `Dir`。
- 环境剥离：`GIT_DIR`、`GIT_WORK_TREE`、`GIT_INDEX_FILE`、`GIT_OBJECT_DIRECTORY`、`GIT_COMMON_DIR`、`GIT_NAMESPACE`、`GIT_CEILING_DIRECTORIES`、`GIT_PREFIX`；注入 `LC_ALL=C`、`GIT_PAGER=cat`；非交互时 `GIT_TERMINAL_PROMPT=0`。
- 版本门在**任何实际动作之前**执行：`git version` 解析 major/minor，`< 2.30` → 退出 2，消息含实际版本与安装提示。
- 错误分类只做一次、集中在 `gitstore.Classify`：网络/认证 → 4；`stale info`/`non-fast-forward`/`failed to push some refs` → 竞争；其余 → 1。业务层不做字符串匹配。

### 身份与仓库定位
- `project_id = lower(root_hex)[:12]`，`root_hex` 来自 `rev-list --max-parents=0 HEAD`；不二次哈希、不加盐。测试向量：`743c420a8e7b11c3382c49dfec71987cb7a6f710` → `743c420a8e7b`。
- `--project-id` 必须是 `^[0-9a-f]{12}$`；与可推导 ID 不一致且无 `--force` → 退出 2，消息同时给出两个 ID 与 `--force` 的含义。
- 浅克隆、无根提交、多根提交且无 `--project-id` → 退出 2，逐条列出检测到的具体原因（不猜、不取“最小根”）。
- linked worktree（`--absolute-git-dir` ≠ 绝对化后的 `--git-common-dir`）→ 退出 3；`--git-common-dir` 可能相对，按 `--show-toplevel` 拼接。
- 对象格式 `sha1|sha256` 由 `rev-parse --show-object-format` 读取并记录；缺失时按 `sha1` 处理仅用于诊断。

### 配置与状态
- 配置格式 YAML，采用 `STACK.md` 的 struct + `,inline map[string]any`；**不**调用 `KnownFields(true)`，未知键保留并在加载时以 stderr 警告列出（`ARCHITECTURE.md` 的 `KnownFields(true)` 与 STACK 冲突，选 STACK：未知键静默丢弃会让用户配置蒸发）。
- 配置与状态路径：`<absolute-git-dir>/doc-connector/{config.yaml,state.json,fetched.json,journal.json,lock,tmp/}`；目录 `0700`，文件 `0600`。
- 原子写：同目录 `.tmp-<rand>` → `Write` → `Sync` → `Close` → `Rename` → 尽力目录 `Sync`；失败清理临时文件；绝不原地截断。
- 锁：`gofrs/flock` 的 `TryLockContext`，250ms 轮询、总等待 10s；超时退出 3 并打印锁文件路径与记录的 PID。内核在进程死亡时释放，不实现 stale-lock 猜测。
- 状态 schema 只含决策事实（`project_id`、`branch`、`synced_commit`、`baseline_tree`、`unresolved`、时间戳）；fetch 时间戳放独立的纯缓存 `fetched.json`，`status` 因此永不写 `state.json`。

### 机器身份
- `machine` = `sanitize(hostname)`：小写 → 非 `[a-z0-9]` 折叠为 `-` → 去首尾 `-` → 截断 32 字符 → 空则 `unknown`；`--machine` 覆盖，覆盖值走同一 sanitize。

### Phase 1 边界内的 init
- `init` 在 Phase 1 解析 `--remote/--project-id/--branch/--docs-dir/--machine/--force` 并完成身份校验；全部通过后以退出码 2 报 `"init: remote bootstrap is not implemented yet"`。**不写 config、不写 exclude、不建 store**——避免“配置已存在但文档未被排除”的危险中间态。
- `INIT-04` 的 `id_source: explicit` 持久化路径由 `internal/config` 的集成测试覆盖；CLI 端到端由 Phase 2 的 init 完成。

### Claude's Discretion
- 具体文件划分、函数命名与错误消息措辞；`flag` 的具体帮助文案；锁等待时长可通过内部常量在测试中缩短。

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- 无既有代码。仓库仅有 `.planning/` 与 `AGENTS.md`。

### Established Patterns
- 研究文档已固定协议：单二进制、shell out 到系统 git、裸镜像、`os.Root`、原子写、lease 推送。

### Integration Points
- `cmd/doc-connector` 只组装版本、信号与 `cli.Run`；后续阶段在 `internal/app/**` 下挂命令编排，`gitstore` 是唯一 git 执行点。

</code_context>

<specifics>
## Specific Ideas

- 研究文档中的命令序列与版本下限（git ≥ 2.30）直接作为实现依据；不得使用 2.30 不存在的选项（`--path-format`、`check-ignore --no-index`）。
- 测试使用真实 git + `t.TempDir()`，并隔离 `HOME`/`GIT_CONFIG_GLOBAL`/`GIT_CONFIG_NOSYSTEM`，避免开发者全局配置污染。

</specifics>

<deferred>
## Deferred Ideas

- `--json` 输出、`doctor`、`.docignore`、多文档目录 → v2 需求，不在本阶段。

</deferred>
