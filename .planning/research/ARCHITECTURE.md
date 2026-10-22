# Architecture Research

**Domain:** 单二进制 CLI（Go）— 用 git plumbing 把项目私有文档目录同步到共享私有仓库中的 `<project_id>/` 子树
**Researched:** 2026-10-22
**Confidence:** HIGH（协议在 git 2.47.3 上逐条实测；命令语义均对照官方文档；Go API 对照 go1.27 文档）

本文只回答“怎么实现”，已定决策（独立私有仓库、根提交哈希作为项目标识、`private-docs/` + `.git/info/exclude`、裸镜像、三方基线、`.theirs.<machine>`）不再讨论。CLI 表面、退出码与功能取舍以 `.planning/research/FEATURES.md` 为准，本文与之对齐。

---

## Protocol Invariants (implementer must not break these)

1. **团队仓库只读**。对团队 `.git` 的写操作仅允许两处：`.git/info/exclude` 与 `.git/doc-connector/`（含 `store.git/`）。所有 git 调用显式 `--git-dir=<abs>`，绝不继承或依赖 cwd 发现仓库（Git 3.0 起 `safe.bareRepository` 默认 `explicit`，cwd 发现裸库会被拒绝）[G-config]。
2. **不存在第二个工作树**。文档仓库只以裸镜像形式存在；从不 `checkout`、从不创建 worktree。所有树组合在临时 index 中完成。
3. **任何 index 操作必须设置 `GIT_INDEX_FILE`**，指向 `.git/doc-connector/tmp/` 下的临时文件，且不设置 `GIT_WORK_TREE`。
4. **远端引用只通过 lease 更新**：`--force-with-lease=refs/heads/<branch>:<expect>`，永不用裸 `--force`。
5. **基线只在成功后推进**：`state.synced_commit` 仅在 push 成功或 pull 完全干净后更新；任何写入前先落盘 journal。
6. **不丢内容**：只修改“本地内容 == 基线内容”的路径；被覆盖/删除的旧内容因此永远能从 store 的基线 blob 重新取出。远端版本永远先落成 `.theirs.<machine>` 或进入 `unresolved`，再谈其它。

---

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────────┐
│ CLI Surface                                                              │
│  argv 解析 · --help/--version · 退出码契约 0/1/2/3/4/5 · stderr 诊断       │
│  init            push           pull          status     log    restore  │
└───────────────┬──────────────────────────────────────────────────────────┘
                │ Request{Command, Flags, Root}
┌───────────────▼──────────────────────────────────────────────────────────┐
│ Application Layer (command handlers)                                     │
│  预检编排 · 计划展示(--dry-run) · 结果格式化 · 退出码映射                  │
└───────┬───────────────────────────────┬──────────────────────────────────┘
        │                               │
┌───────▼───────────────┐   ┌───────────▼──────────────┐   ┌───────────────┐
│ Sync Engine           │   │ History Service          │   │ Init Service  │
│  plan(3-way) · apply  │   │  log · restore · blob IO │   │ guard+exclude │
└───┬────────┬──────────┘   └───────────┬──────────────┘   └──┬────────────┘
    │        │                          │                     │
┌───▼───┐ ┌──▼─────────────┐ ┌──────────▼──────┐ ┌────────────▼───────────┐
│ Tree  │ │ Conflict       │ │ Git Store        │ │ Repo Identity          │
│Builder│ │ Resolver       │ │ (single adapter) │ │ root commit → pid      │
│splice │ │ artifacts/     │ │ exec/fetch/push  │ │ shallow/multi-root     │
│mktree │ │ unresolved set │ │ refs/objects     │ │                        │
└───┬───┘ └──┬─────────────┘ └──────────┬───────┘ └────────────┬───────────┘
    │        │                          │                      │
┌───▼────────▼──────────┐  ┌────────────▼───────────┐  ┌───────▼───────────┐
│ Local Model           │  │ Config / State         │  │ Filesystem Guard  │
│ Ignore Filter         │  │ config.yaml ·          │  │ os.Root · atomic  │
│ Scanner(hashes/modes) │  │ state.json · journal   │  │ lock · path rules │
└───────────────────────┘  └────────────────────────┘  └───────────────────┘
                │
┌───────────────▼──────────────────────────────────────────────────────────┐
│ External                                                                 │
│  system git (>=2.30)   ·   private remote (SSH/HTTPS/file://)             │
│  project worktree (private-docs/)   ·   OS filesystem                    │
└──────────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | 拥有什么 | 实现要点 |
|---|---|---|
| **CLI dispatch** (`internal/cli`) | argv→Request、flag 校验、退出码、人类可读输出 | 手写 flag 解析或 `flag` 子命令；每个命令一个 `Run(ctx, Request) (ExitCode, error)`；不打印 git 原始 stderr，只附在错误尾部 |
| **Repo identity** (`internal/project`) | 定位工作树/公共 git 目录、根提交推导、`project_id`、linked-worktree 拒绝 | `rev-parse` 系列 + `rev-list --max-parents=0`；纯函数 `DeriveID(rootSHA) string` |
| **Config/State** (`internal/config`, `internal/state`) | 解析/校验 `config.yaml`、`state.json`、`journal.json`、`lock`；原子写 | YAML 只读人类配置（`KnownFields(true)`），其余一律 JSON；写入走 temp+fsync+rename |
| **Git store** (`internal/gitstore`) | 唯一执行 `git` 的地方；env 清洗；对象导入；fetch/push/ref；blob 读取；错误分类 | 结构化 argv、cwd 固定为项目根、`--git-dir` 绝对路径；`GitError{Args, Code, Stderr}` |
| **Tree builder** (`internal/trees`) | 本地扫描结果 → 项目子树 tree → 与原远端根 tree 拼接 → 新根 tree | 子树用临时 index 整棵重建（`write-tree`）；根层用 `ls-tree`/`mktree` 只替换 `<pid>` 一个条目 |
| **Sync engine** (`internal/sync`) | 三方分类、动作计划、apply、重试循环、基线推进 | 纯函数 `Classify(base, local, remote) []PathAction`；I/O 只发生在 `Apply(plan)` |
| **Conflict resolver** (`internal/sync/conflict`) | `.theirs.<machine>` 命名与写入、`unresolved` 集合维护、`--resolve` 语义 | 命名/冲突去重/长度回退；只写副本，不改本地文件 |
| **Filesystem guard** (`internal/fsx`) | `os.Root` 封装、路径校验、原子写、目录清理、锁 | 所有 docs 目录读写经 `*os.Root`；`filepath.IsLocal` + 自定义规则（见 Safety） |
| **Ignore filter** (`internal/ignore`) | 内置垃圾列表 + `*.theirs.*` + 配置追加项；匹配与统计 | 纯函数 `Match(relPath string) (bool, Reason)`；在 hash 之前过滤 |
| **History service** (`internal/history`) | `log`、`restore`、blob 流式读取、路径历史 | 只读 `store.git`，可离线；`cat-file --batch` 流式输出到临时文件 |

**依赖方向**：`cli → app → {sync, history, init} → {trees, gitstore, fsx} → {config, state, ignore, project}`。`gitstore` 是唯一可以生成 `exec.Command("git", …)` 的包；其它包通过接口拿数据。这样协议测试可以用“记录 argv + 回放输出”的假 store，而集成测试用真实 git + `t.TempDir()`。

### Exit code mapping

| Code | 含义 | 架构中的产生点 |
|---|---|---|
| 0 | 成功或无操作（push 无变化、pull 无动作） | `Plan` 为空；push 的 `Troot == tree(REMOTE)` |
| 1 | 需要用户决策：diverged、冲突、adoption 歧义、restore 覆盖拒绝、远端历史被改写 | `Classify` 出现 `C`；push 的 `clobber` 集合非空；`unresolved` 非空时的 push |
| 2 | 用法/配置：未知 flag、缺 `--remote`、浅克隆或多根无 `--project-id`、git < 2.30、config schema 错误、内嵌凭据 | 启动阶段与 `config.Load` |
| 3 | 安全/前置条件：docs 被跟踪、未被 ignore、docs 是 symlink、linked worktree、路径非法、大小写碰撞、锁被占用 | `project.Guard`、`fsx.Validate`、`state.Lock` |
| 4 | 远端/网络/认证：fetch/push 失败、重试耗尽 | `gitstore` 的错误分类 |
| 5 | 本地 I/O/完整性：rename/fsync 失败、对象缺失、超限文件、scan 中 OID 变化 | `fsx`、`trees`、`sync.Apply` |

退出码由 `app` 层统一映射；`gitstore` 只返回带分类的错误，不决定进程状态。

### Exit code 与 `--dry-run`

`--dry-run` 走同一套 `Plan` 与 `Classify`，返回“真实运行会返回的码”（例如会冲突则返回 1），但不执行 apply、不写 ref/state。

### Module Dependency Graph

```
cmd/doc-connector
        │
      cli ──────────────┐
        │               │
   app/init  app/push  app/pull  app/status  app/log  app/restore
        │        │         │         │          │         │
        └────────┴────┬────┴─────────┘          │         │
                      │                        │         │
                 sync.Engine ──────────────────┘         │
                  │      │                               │
          trees.Build   conflict.Resolver          history.Service
                  │      │                               │
                  └──────┴──────────┬────────────────────┘
                                    │
                              gitstore.Repo
                                    │
                    ┌───────────────┼───────────────┐
                 fsx.Root        state.Store      config.Load
                    │
                 ignore.Filter + project.Identity
```

---

## Recommended Project Structure

```
doc-connector/
├── cmd/doc-connector/main.go        # 仅组装：版本、信号、ExitCode
├── internal/
│   ├── cli/                         # 命令解析、flag、输出与退出码
│   │   ├── root.go                  # 全局 flag (--dry-run, --verbose, --json 预留)
│   │   ├── init.go push.go pull.go status.go log.go restore.go
│   │   └── exit.go                  # 0/1/2/3/4/5 常量与映射
│   ├── app/                         # 命令级编排（guard → plan → apply → report）
│   │   ├── initapp/  pushapp/  pullapp/  statusapp/  logapp/  restoreapp/
│   ├── project/                     # identity.go root.go exclude.go guard.go
│   ├── config/                      # schema.go load.go (YAML, KnownFields)
│   ├── state/                       # state.go journal.go atomic.go lock.go
│   ├── gitstore/                    # exec.go fetch.go push.go refs.go objects.go tree.go blob.go
│   ├── trees/                       # build.go splice.go entry.go parse.go
│   ├── sync/                        # classify.go plan.go apply.go retry.go
│   │   └── conflict/                # name.go write.go unresolved.go
│   ├── fsx/                         # root.go atomic.go dirs.go validate.go
│   ├── ignore/                      # builtin.go match.go
│   └── history/                     # log.go restore.go
├── testdata/                        # 协议回放样本（ls-tree/mktree/porcelain 输出）
└── go.mod                           # go 1.26（os.Root 的 Rename/WriteFile/MkdirAll 下限是 1.25）
```

**Go 版本说明**：`os.Root` 在 Go 1.24 引入（初始只有 `Open/Create/Mkdir/Stat` 等）[GO-1.24]，`Root.Rename`/`Root.WriteFile`/`Root.MkdirAll`/`Root.RemoveAll` 在 Go 1.25 才出现（go1.25 release notes 明确列出）[GO-1.25]。模块声明 `go 1.26` 而非 1.25，是按 Go 的“支持最近两个大版本”策略留出安全余量；若必须兼容 1.25，功能不受影响，只需把 `go.mod` 改为 1.25。

**Rationale**

- `gitstore` 与 `trees` 分离：前者回答“git 怎么说”，后者回答“树长什么样”。分类算法（`sync`）因此完全不接触进程与文件系统，可用表驱动单测覆盖三方表的全部 15 种状态。
- `fsx` 集中所有路径规则：pull/restore/scan 三处写入必须共用同一套校验，否则会重现“一处放宽、一处收紧”的漏洞。
- `state` 独立于 `config`：`config.yaml` 是用户可编辑且会被 `init` 重写的；`state.json` 是机器数据，永不手改，schema 演进靠 `schema:` 字段。

---

## Architectural Patterns

### Pattern 1: Splice-by-object (no worktree, no checkout)

**What:** 本地文件 → blob（`hash-object`）→ 项目子树 tree（临时 index + `write-tree`）→ 用远端根 tree 的条目列表替换 `<project_id>` 一项（`ls-tree` + `mktree`）→ `commit-tree`。
**When to use:** 每次 push；也用于构造“如果现在 push 会得到什么”的 `--dry-run` 预览（不写 ref、不 push）。
**Trade-offs:** 比“检出远端到临时目录再 add”快一个数量级、且不产生工作树漂移；代价是要自己维护 index 与路径前缀，且必须理解 `mktree` 的输入格式（非递归 `ls-tree` 输出格式）。

```go
// 伪代码：一次 push 的树构建
sub  := indexFromEntries(localEntries)      // read-tree --empty; update-index --index-info; write-tree
root := spliceRoot(remoteCommit, pid, sub)  // ls-tree -z <remote>; 去掉 <pid>; mktree -z
```

### Pattern 2: Plan → Apply（两阶段，可 dry-run）

**What:** `Classify` 只产出 `Plan{Paths []PathAction, Subtree map[string]Entry, Conflicts []Conflict}`；`Apply` 才写文件/写 ref。`--dry-run` 打印 Plan 后直接返回“真实运行会返回的退出码”。
**When to use:** push、pull、restore、init 的 adopt 分支。
**Trade-offs:** 多一次内存遍历；换来三处命令共享同一分类器（FEATURES 的硬要求：不能各命令各自判断），以及崩溃前可审计的计划。

### Pattern 3: Derived baseline, cached

**What:** 基线不是“一份文件清单”，而是 `state.synced_commit` 这个提交；基线树恒等于 `rev-parse <synced_commit>:<project_id>`。`state.baseline_tree` 只是缓存，加载时校验，不匹配就以提交为准。
**When to use:** 始终。
**为什么:** 文件清单会与 git 真实状态漂移（尤其是 `restore`、外部改动、崩溃后）。提交是唯一自洽的事实源，且天然支持“基线落后于远端但内容已收敛”的情形。

### Pattern 4: Guard before every mutation

**What:** 每个写命令（init/push/pull/restore）在执行任何写操作前跑同一组不变量：工作树内、非 bare、非 linked worktree、docs 目录真实存在且非符号链接、docs 未被跟踪、docs 被 ignore、`project_id` 三向一致（`config.project.id == state.project_id`，且 `id_source=derived` 时还等于 `Derive(HEAD)`）、store 位于 `.git/doc-connector/`、无遗留 journal（或先恢复）。任一不符 → 退出 3，消息指出具体检查项。
**When to use:** 无条件。
**Trade-offs:** 每次多 4~6 次 git 调用（毫秒级）；这是产品存在理由，不可优化掉。

### Pattern 5: Append-only history, lease-guarded ref

**What:** 每次同步都新建 commit，parent 是“刚 fetch 到的远端 tip”。远端引用更新使用 `--force-with-lease=<ref>:<expect>`，`expect` 是我们读到的远端值。
**When to use:** push；空远端时 `expect` 为空串（语义：该 ref 必须不存在）。
**Trade-offs:** 天然产生线性历史；代价是竞争时要重算（见 Concurrency）。

---

## Data Flow

### On-disk Layout

```
<project-root>/
├── .git/
│   ├── info/exclude                 # 追加受管块（唯一允许改的共享仓库文件）
│   └── doc-connector/
│       ├── config.yaml              # 用户可编辑
│       ├── state.json               # 机器状态（原子写）
│       ├── fetched.json             # 纯缓存：最近一次 fetch 的时间/commit
│       ├── journal.json             # 仅 apply 期间存在
│       ├── lock                     # O_EXCL 单实例锁
│       ├── tmp/                     # 0700；索引、staged blob、回滚暂存
│       └── store.git/               # 裸镜像（docs 仓库）
│           ├── objects/ refs/ …
│           └── refs/remotes/origin/<branch>
└── private-docs/                    # 同步内容（被 info/exclude 排除）
```

`store.git` 的 ref 约定：

| ref | 含义 | 谁写 |
|---|---|---|
| `refs/remotes/origin/<branch>` | 最近一次成功 fetch 或 push 后的远端 tip | fetch / push 成功后 `update-ref` |
| `refs/doc-connector/pending` | 已构造、尚未推送成功的 commit（崩溃后可复用/审计） | push 构造后写入，成功后删除 |

不配置 `remote.origin.url`：URL 来自 `config.yaml`，每次显式传给 `fetch`/`push`。这样 `git remote -v` 不会泄露（配置本就隐藏），也避免多项目共享同一 store 时的 URL 漂移。

### Project identity: exact derivation (frozen 2026-10-22)

`PROJECT.md` 的 Key Decisions 写的是“项目标识 = 根提交 SHA 前 12 位”，而早期 brief 写的是 `sha256(root commit SHA)` 前 12 位。两者都确定性、都跨克隆稳定，但**写入一次、永久生效**：一旦有项目推到远端，改公式就等于换目录。最终裁定采用 `PROJECT.md` 的字面定义（理由：少一次变换、可直接用 `git log --format=%H` 肉眼核对、与 Key Decisions 表一致），实现不得自行变通：

```
input   : root_hex = 小写 40 位（SHA-1）或 64 位（SHA-256）hex，来自 `git rev-list --max-parents=0 HEAD`
          （恰好一个根；多根 → 拒绝并要求 --project-id，见下表）
algorithm: 直接截取前 12 个字符，不哈希、不加盐、不做大小写变换
output  : 12 个小写 hex 字符
override: --project-id 直接使用（校验 ^[0-9a-f]{12}$），配置记 id_source: explicit
test vec: root 743c420a8e7b11c3382c49dfec71987cb7a6f710
          id   = 743c420a8e7b
```

被明确排除的读法（都能“工作”，但与上面的 id 不同，禁止混用）：`sha256(ASCII(root_hex))[:12]`（早期 brief；对上面的 root 会得到 `8d818c0fc915`）、`sha256(20 字节二进制 SHA)[:12]`、任何加盐/加项目名的变体。若日后必须切换，唯一安全路径是 `--project-id` 或人工迁移远端目录；代码不做自动探测，也不会“猜”另一个公式。

对象格式提示：`git rev-parse --show-object-format` 为 `sha256` 的仓库，根提交 hex 是 64 位，截取前 12 位得到的 id 与同一历史在 SHA-1 仓库中的 id **不同**。`config.project.object_format` 记录它，`status` 在两者不一致时给出诊断（不自动改写 id）。

### init

```
1) 定位与预检
   git -C <cwd> rev-parse --show-toplevel                     # [G-rev-parse]
   git -C <cwd> rev-parse --is-inside-work-tree               # 必须 true
   git -C <cwd> rev-parse --is-bare-repository                # 必须 false
   git -C <cwd> rev-parse --absolute-git-dir                  # 绝对路径
   git -C <cwd> rev-parse --git-common-dir                    # 可能相对；用 toplevel 拼绝对
   若 abs(git-dir) != abs(common-dir) → 退出 3（linked worktree，两个工作树会争同一份 state）

2) 身份
   git -C <root> rev-parse --is-shallow-repository            # true 且无 --project-id → 退出 2
   git -C <root> rev-parse --show-object-format               # sha1 | sha256（仅记录/诊断）
   git -C <root> rev-list --max-parents=0 HEAD                # 根提交集合
     0 个 → 空仓库：无 --project-id 则退出 2
     1 个 → root
     >1   → 拒绝并要求显式 --project-id（退出 2）；不取最小者（歧义、不可预测）
   pid = root_hex_lower[:12]                        或 --project-id（校验 ^[0-9a-f]{12}$）
        root_hex_lower 是 git 输出的 40/64 位小写 hex，直接截取前 12 位；不做二次哈希。
        （2026-10-22 冻结：采用 PROJECT.md 字面定义，早期 brief 的 sha256 方案作废；详见上节）

3) Guard 的写前子集（必须在碰 `info/exclude` 之前）
   git -C <root> ls-files -z -- private-docs/                  # 必须为空，否则退出 3
   （此刻 exclude 可能尚未写入，所以不能在这里要求 check-ignore 通过；只验证“未被跟踪”
     这一条不可逆事实。文档已被跟踪时，连写 exclude 都没有意义。）

4) 远端探测（唯一一次“猜分支”）
   git ls-remote --symref --exit-code <url> HEAD              # [G-ls-remote]
     exit 0 → 输出含 "ref: refs/heads/<X>\tHEAD" → branch = X（--branch 覆盖）
     exit 2 → 空远端 → branch = --branch 或 "main"
     exit 128 → 网络/认证错误 → 退出 4

5) store 引导
   mkdir -p .git/doc-connector
   git init --bare -b <branch> .git/doc-connector/store.git    # [G-init]
   git --git-dir=<store> fetch --no-tags --prune <url> \
        '+refs/heads/*:refs/remotes/origin/*'                  # 空远端也 exit 0 [G-fetch]
   REMOTE = git --git-dir=<store> rev-parse --verify -q refs/remotes/origin/<branch>

6) 采用（adoption）判定
   R = TreeOf(REMOTE, pid)   ;   L = Scan(private-docs/)
   R 空 & L 空        → 新项目，base = nil
   R 空 & L 非空      → base = nil，提示 push
   R 非空 & L 空      → base = REMOTE，提示 pull
   R 非空 & L 非空
      逐路径相等      → base = REMOTE
      有差异          → 退出 1（ambiguous adoption），除非 --adopt-local
      --adopt-local   → base = nil 且 config.project.adoption = "local"（首次 push 允许整段替换）

7) 隐身 exclude（幂等，带标记块）
   <common>/info/exclude 追加：
       # doc-connector:begin
       /private-docs/
       # doc-connector:end
   若已存在同内容块 → 跳过；存在不同内容块 → 原位替换（只动自己那段）

8) 写后校验（Guard 全量，必须在第一次 push 前通过）
   git -C <root> ls-files -z -- private-docs/                  # 必须为空
   git -C <root> check-ignore -q -- private-docs/              # 必须 exit 0 [G-check-ignore]
   （顺序不可交换：`check-ignore` 对已被跟踪的路径返回 1；先确认未跟踪，结论才只反映 exclude 规则）

9) 落盘
   config.yaml（原子） → state.json（原子）
   输出：project / remote(branch) / base / 本地与远端文件数 / 下一步命令
```

> `--project-id` 与 `--machine`、`--branch`、`--adopt-local` 都是 init 的 flag，不新增子命令。任何一步失败都不写 config（先写 config 会让后续命令在错误状态下运行）。

### status

```
1) Guard（只读子集：不要求 journal 为空）
2) L = Scan(docs)            # hash-object（不带 -w），按忽略规则过滤
3) 默认 fetch（--offline 跳过）
   git --git-dir=<store> fetch --no-tags --prune <url> '+refs/heads/*:refs/remotes/origin/*'
   成功 → 写 fetched.json（原子；纯缓存，不是 state）
   失败 → 用缓存 tip，打印 "fetched <age> ago"，最终退出 4
4) B = TreeOf(state.synced_commit, pid) ; R = TreeOf(REMOTE, pid)
5) plan = Classify(B, L, R)
6) 汇总：clean / ahead / behind / diverged + 每文件 A/M/D/C；C 行带 artifact 路径
```

`status` 永不写 `state.json`；fetch 只动 `store.git` 的 `refs/remotes/origin/*`。

### push

```
0) Guard；要求 unresolved 为空，除非给出 --resolve <path>…
   --resolve 只接受当前 unresolved 中的路径（否则退出 2）；它表示“这些路径以本地为准”：
   清除对应 unresolved 条目，并把它们从下面的 clobber 集合中排除（远端版本仍留在历史里，可 restore）
1) L = Scan(docs)                        # 见“对象导入”；此处 blob 已写入 store
2) fetch（同 status）；REMOTE，R
3) B = TreeOf(state.synced_commit, pid)
   若 B 非空且 REMOTE 存在：
       git --git-dir=<store> merge-base --is-ancestor <B> <REMOTE>
       exit 1 → 远端历史被外部改写（B 不再是 REMOTE 的祖先）→ 退出 1，要求人工确认/--adopt-local
   前置条件（push 只做本地→远端，不做合并）：
       clobber = { p | !eq(R[p], B[p]) && !eq(L[p], R[p]) } \ resolvedPaths
       clobber 非空 → 退出 1，列出这些路径（远端改了、本地没有同样改动），提示先 pull
       B == nil 且 R 非空且 adoption != local → 退出 1（ambiguity，见 init）
   直观含义：push 允许“远端已改、而本地恰好也变成同一内容”的路径（此时用 L 覆盖不丢任何东西），
   但绝不允许覆盖任何“远端有、本地没有”的版本。R == B 是最常见的满足方式；其它项目的改动在
   <pid>/ 之外，splice 时原样保留。

4) 子树：
   Tsub = BuildSubtree(L)                # 若 L 为空 → 空树，splice 时删除 <pid> 条目
5) 根树：
   Troot = Splice(REMOTE, pid, Tsub)
   若 Troot == tree(REMOTE) → 无变化，退出 0（不产生空提交）
6) 提交：
   C = git commit-tree Troot [-p REMOTE] -m <message>     # 消息走 stdin [G-commit-tree]
   git update-ref refs/doc-connector/pending C
7) 推送（带 lease）：
   git --git-dir=<store> push --porcelain \
       --force-with-lease=refs/heads/<branch>:<expect> \
       <url> <C>:refs/heads/<branch>                       # [G-push]
   expect = REMOTE（未出生远端时为空串 → “该 ref 必须不存在”）
8) 结果：
   成功 → update-ref refs/remotes/origin/<branch> C
          state.synced_commit = C ; baseline_tree = rev-parse C:<pid> ; 清 pending ; 原子写
   拒绝 → 见重试循环
```

`--dry-run` 的 push 语义（“不产生可观察副作用”的严格实现）：扫描用 `hash-object`（不带 `-w`）；子树与根树用 `write-tree --missing-ok` 与 `mktree --missing` 计算（两者关闭对象存在性校验，实测可产出“引用缺失 blob 的树”）；不 `commit-tree`、不写 ref、不 push。输出“将以 parent `<REMOTE>` 建提交，tree `<Troot>`，+a ~m -d”。注意：`write-tree`/`mktree` 仍会在 store 里落下少量 tree 对象（内容寻址、不可见于任何 ref，可被 `gc` 回收）——这正是能离线预览的原因。

重试循环（`sync/retry.go`）：

```go
for attempt := 0; attempt < 3; attempt++ {
    res := store.Push(commit, expect)
    if res.OK { advanceBase(commit); return nil }
    if res.Class != LeaseRejected && res.Class != NonFastForward { return err } // 认证/网络 → 退出 4

    expect = store.Fetch()                  // 重新读远端（不能复用旧值）
    R2 := store.TreeOf(expect, pid)
    if !clobberFree(B, L, R2) {             // 竞争窗口里本前缀出现了“远端有、本地没有”的版本
        return ErrDiverged                  // 退出 1，提示 pull
    }
    // 其余情况（通常只是别的项目推了新提交，或远端已收敛到本地内容）：在最新 tip 上重建
    Troot = Splice(expect, pid, Tsub)       // Tsub 可复用（L 未变）
    commit = Commit(Troot, expect, msg)     // 旧 commit 未发布，丢弃无副作用
    store.UpdateRef(pending, commit)
    sleep(100ms << attempt + jitter)
}
return ErrRaceRetriesExhausted              // 退出 4
```

重试期间若本地文件被改动（重扫后 `L` 的 OID 集合变化）→ 中止（退出 5），绝不推送用户没看过的内容。

### pull

```
1) Guard（允许存在未解决冲突，但要先恢复 journal）
2) fetch → REMOTE ; R ; B = TreeOf(state.synced_commit, pid)
3) L = Scan(docs)
4) actions = Classify(B, L, R)             # 见三方表
5) 若有需要写入/删除的动作：
   a. 先把所有远端 blob 流式落到 tmp/op-<rand>/（cat-file --batch，见下文）
   b. 写 journal.json（含每个动作、staged 路径、state_next 的临时文件路径）
   c. 逐条 apply：写临时文件→fsync→rename；删除→rename 进 tmp 回收站后再 unlink
   d. 写 state（原子），删 journal，删 tmp
   任一动作失败 → 按 journal 回滚（旧内容可从基线 blob 重建，见 Safety）
6) 冲突路径：本地不动，写 <path>.theirs.<machine>（远端有内容时），记入 state.unresolved
7) 基线推进：仅当 unresolved 为空且所有动作成功 → synced_commit = REMOTE
   否则保持原基线（分类器用 “local == remote ⇒ converged” 保证下一次 status 不误报）
8) 输出 applied/conflicted 计数；有冲突退出 1
```

### log

```
git --git-dir=<store> log --format='%H%x00%aI%x00%an%x00%s' \
    [--name-only] [-- <pid>/[<subpath>]]                 # [G-log]
```
默认从 `refs/remotes/origin/<branch>` 出发（不是 `state.synced_commit`，也不是 orphan 的 pending）；`log` 本身**不联网**（离线优先的审计轨迹），输出里带 `fetched.json` 的 `at` 年龄；需要最新时先跑 `status` 或 `pull`。`--path` 在项目前缀内进一步过滤。提交消息含 trailer：`Doc-Connector-Project`、`Doc-Connector-Machine`、`Doc-Connector-Files`，`log --format` 可直接取用。

### restore

```
1) Guard（只读 + 目标路径校验）
2) rev = --from 或 REMOTE（默认远端 tip）
   若 <rev>:<pid>/<path> 是 blob：
       git --git-dir=<store> cat-file blob <rev>:<pid>/<path> > tmp/file
   若该路径在 rev 中已删除：
       last = git --git-dir=<store> log --format=%H -1 <rev> -- <pid>/<path>
       再从 last 取 blob（“恢复被删除的文件”）
3) 目标：
   默认写回 private-docs/<path>；已存在且内容不同 → 除非 --force 否则退出 1
   --to <dir> → 写到该目录，目标由用户负责
4) 原子写；若 state.unresolved 含该路径且恢复内容 == 远端 blob → 清除该条
```

`restore --force` 是“远端胜出”的正式解析手段（FEATURES 契约）；它只改本地文件与 unresolved，不改基线。

### Key Data Flows

1. **内容字节流**：`private-docs/<p>` → `hash-object -w --no-filters` → store blob（字节精确，无 eol/filter）→ index → tree → commit → remote。
2. **远端字节流**：remote → `fetch` → store object DB → `cat-file --batch` → `tmp/op-*/` → `rename` → `private-docs/<p>`。文件从不经过内存整块（单文件流式复制）。
3. **元数据流**：`config.yaml`（人）→ 每次命令读取；`state.json`（机器）→ 只在成功点写；`journal.json`（事务）→ apply 期间存在。

### State Management

```
config.yaml ──(每次启动读取, schema+语义校验)──► 内存 Config
state.json  ──(读取+派生校验)──────────────► 内存 State + Baseline(commit→tree)
      ▲                                            │
      └────────(成功点原子写)───────────────────────┘
journal.json ──(apply 前写, 完成后删)──► 崩溃恢复的唯一依据
```

---

## State Schema

### `config.yaml`

```yaml
schema: 1
remote: "git@github.com:me/private-docs.git"   # 任意 git 可访问 URL；不接受内嵌凭据（警告并拒绝）
branch: "main"
docs_dir: "private-docs"                        # 相对项目根；init 后只读
machine: "laptop"                               # 冲突后缀与提交 trailer；默认 sanitize(hostname)
project:
  id: "3f2a9c1b7d4e"
  id_source: "derived"                          # derived | explicit
  root_commit: "7c1d…（40 hex）"                 # explicit 时为空
  object_format: "sha1"                         # sha1 | sha256；仅诊断用
  adoption: "none"                              # none | local（仅 --adopt-local 写入）
ignore:
  extra: []                                     # v1.x 的 .docignore 预留；v1 仅接受内置+此列表
limits:
  warn_bytes: 52428800                          # 50 MiB
  max_bytes: 104857600                          # 100 MiB，--allow-large 覆盖
  max_path_chars: 240
author:                                         # 可选；缺省用合成身份
  name: "doc-connector"
  email: "doc-connector@laptop"
created_at: "2026-10-22T12:00:00Z"
created_by: "laptop"
```

校验规则：未知字段报错（`KnownFields(true)`）；`project.id` 必须 12 位小写 hex；`branch` 必须通过 `git check-ref-format --branch <name>` 校验（合法返回 0，非法返回非 0——实测无效名是 128，因此按“非 0 即无效”处理，不假定为 1）[G-check-ref-format]；`remote` 中出现 `user:pass@` 或 `token@` → 退出 2 并提示改用 credential helper。

### `state.json`

```json
{
  "schema": 1,
  "project_id": "3f2a9c1b7d4e",
  "branch": "main",
  "synced_commit": "9c1e4a2…",
  "baseline_tree": "a1b2c3…",
  "last_sync_at": "2026-10-22T12:02:40Z",
  "last_sync_kind": "push",
  "unresolved": [
    {"path": "notes/plan.md", "kind": "both-modified",
     "theirs": "notes/plan.md.theirs.laptop",
     "remote_oid": "…", "base_oid": "…", "at": "2026-10-22T11:58:00Z"}
  ]
}
```

另有一个**纯缓存**文件 `.git/doc-connector/fetched.json`（可随时删除，不属于 state）：

```json
{"schema":1,"at":"2026-10-22T12:03:11Z","commit":"4a7b91c…","by":"status"}
```

`status --offline` 用它的 `at` 显示“fetched <age> ago”。这样 `status` 可以刷新 fetch 时间戳却仍然**不写 `state.json`**；`state.json` 只保存会影响同步决策的事实。

**基线定义（唯一权威）**：

```
baseline(commit, pid) := ls-tree -r <commit>:<pid>   （不存在则为空 map）
synced_commit = null  ⇒ baseline 为空（首次同步 / adopt-local）
```

加载时若 `baseline_tree != rev-parse synced_commit:pid`，以提交为准并 warn（`baseline_tree` 仅用于快速判断“远端是否动过本前缀”）。

另有两条**拒绝写入**的一致性检查：`state.project_id != config.project.id` 或 `state.branch != config.branch` → 退出 2，提示“配置与状态不一致，重新 init 或恢复备份”，绝不在不匹配的仓库/分支上同步。

**原子写协议**（所有 JSON 与 config）：

1. 在目标同目录创建 `.tmp-<pid>-<rand>`，`0600`（config `0644` 亦可，但默认更严）。
2. 写入 → `f.Sync()` → `Close()`。
3. `os.Rename(tmp, target)`（同目录；Windows 上 Go 用替换语义，但非 Unix 平台不保证原子）。
4. 尽力 `fsync` 目录（Windows 无法对目录 `Sync`，忽略 `EINVAL`/`EPERM`）。
5. 失败清理 tmp；目标文件永远要么旧要么新。

### `journal.json`（仅 pull/restore 的 apply 期间）

```json
{
  "schema": 1,
  "op": "pull",
  "started_at": "2026-10-22T12:03:00Z",
  "project_id": "3f2a9c1b7d4e",
  "remote_commit": "4a7b91c…",
  "ops": [
    {"action": "write", "path": "notes/a.md", "mode": "100644",
     "oid": "…", "staged": "tmp/op-7f3a1c/a.md"},
    {"action": "delete", "path": "notes/old.md", "staged": "tmp/op-7f3a1c/.trash/old.md"},
    {"action": "conflict", "path": "notes/plan.md", "theirs": "notes/plan.md.theirs.laptop",
     "staged": "tmp/op-7f3a1c/plan.md"}
  ],
  "state_next": "tmp/op-7f3a1c/state.next.json"
}
```

恢复算法（进程启动时若发现 journal）：

```
for op in ops:
    write    : 目标已存在且 blob 相同 → skip；否则 rename(staged, target)
    delete   : 目标不存在 → skip；否则 rename(target, staged)（保留内容，可回滚）
    conflict : artifact 已存在且内容相同 → skip；否则 rename(staged, artifact)
写 state（从 state_next 原子 rename）；删 journal；删 tmp 目录
```

因为 `staged` 在 journal 删除前一直保留，恢复是**前滚幂等**的；不需要“回滚到上一状态”的分支。若用户在崩溃后手动改过文件，恢复只会补做未完成的动作，不会覆盖已存在且内容相同的目标。

### `lock`

```
O_CREATE|O_EXCL|O_WRONLY, 0600
{"schema":1,"pid":1234,"machine":"laptop","cmd":"push","started_at":"…"}
```

- 已存在 → 读内容：`machine == 本机` 且 `pid` 不存活 → 删除并重试一次；否则退出 3，打印持有者。
- 进程退出/崩溃：文件残留，靠上述 PID 检测清理（Windows 上同样适用）。
- Unix 额外加 `syscall.Flock`（stdio 锁在同一 fd）可让内核在进程死亡时自动释放，文件残留不再影响判断；Windows 若引入 `golang.org/x/sys/windows` 可用 `LockFileEx` 达到同等效果。v1 以 O_EXCL+PID 为基线行为，两种增强都不改变 CLI 语义。
- 锁覆盖整个命令（含网络），不覆盖跨项目：`store.git` 在项目内，不同项目天然不共享锁。

---

## Git Plumbing Specifics

### Command reference (all verified on git 2.47.3)

| 目的 | 命令 | 关键点 / 版本 |
|---|---|---|
| 发现与预检 | `rev-parse --show-toplevel / --is-inside-work-tree / --is-bare-repository / --absolute-git-dir / --git-common-dir / --is-shallow-repository / --show-object-format` | `--git-common-dir` 可能返回相对路径；`--path-format`（绝对化）是 **2.31** 新增，2.30 下限下不可用，自己拼 [G-rev-parse][G-2.31] |
| 根提交 | `rev-list --max-parents=0 HEAD` | `--max-parents=0` 即“所有根提交” [G-rev-list] |
| 远端探测 | `ls-remote --symref --exit-code <url> HEAD` | `--symref` 只显示 HEAD 的符号引用；无匹配 ref 时 `--exit-code` 返回 2 [G-ls-remote] |
| 空远端分支回退 | `git init --bare -b <branch>` | 未指定时默认仍是 `master`（Git 3.0 才改 `main`），所以必须显式 `-b` [G-init] |
| 取回 | `fetch --no-tags --prune <url> '+refs/heads/*:refs/remotes/origin/*'` | 空远端上**显式** `refs/heads/<b>` refspec 会 `fatal: couldn't find remote ref`（实测 128）；glob 则 exit 0 [G-fetch] |
| 导对象 | `hash-object -w --no-filters --stdin-paths` | 每行一个路径，按顺序输出 OID；`--no-filters` 关闭 eol/filter，保证字节精确；2.47 无 `-z`，含换行路径需逐文件回退 [G-hash-object] |
| 建索引 | `read-tree --empty` + `update-index -z --index-info` | `--index-info` 自己读 stdin，**不能**与 `--stdin` 同用；输入格式 `mode SP sha1 TAB path`；`mode 0` 表示删除该路径 [G-update-index] |
| 写树 | `write-tree` | 索引为空 → 空树 `4b825dc6…`；`--missing-ok` 关闭对象存在性检查：仅 `--dry-run` 预览可用，正式路径必须保持校验 [G-write-tree] |
| 根层拼接 | `ls-tree -z <rev>` + `mktree -z` | `ls-tree` 非递归输出正好是 `mktree` 输入格式（`<mode> SP <type> SP <oid> TAB <name>`）；`mktree` 会自行排序并校验对象存在 [G-ls-tree][G-mktree] |
| 提交 | `commit-tree <tree> -p <parent> -m …` | 消息可走 stdin；`-p` 可重复；根提交无 parent [G-commit-tree] |
| 本地 ref | `update-ref <ref> <new> [<old>]` | 带 `<old>` 为 CAS；`<old>` 为 40 个 0 或空串表示“必须不存在” [G-update-ref] |
| 推送 | `push --porcelain --force-with-lease=refs/heads/<b>:<expect> <url> <sha>:refs/heads/<b>` | `<expect>` 空串 = 该 ref 必须不存在；拒绝时 porcelain 输出 `!\t<src>:<dst>\t[rejected] (stale info)` 且 exit 1 [G-push] |
| 读 blob | `cat-file blob <rev>:<path>` / `cat-file --batch` | `<rev>` 用完整 40 hex，首个冒号即分隔符，路径含 `:`/空格安全；`--batch` 帧为 `<oid> <type> <size>\n` + 内容 + `\n` [G-cat-file] |
| 祖先判断 | `merge-base --is-ancestor A B` | exit 0 是祖先、1 不是；用于“是否可快进” [G-merge-base] |
| 计数 | `rev-list --count A..B` | ahead/behind 提交计数 [G-rev-list] |
| 历史 | `log --format=… [--name-only] -- <pid>/` | 路径过滤限定项目前缀 [G-log] |
| 隐身校验 | `check-ignore -q -- <path>` | 必须先确认路径未被跟踪；`--no-index` 不在 2.30/2.31 文档中，不要用 [G-check-ignore] |
| 回收 | `gc --auto --quiet` | 只在成功同步后调用；`--auto` 无需要时直接退出 [G-gc] |

### Environment discipline (`internal/gitstore/exec.go`)

```go
env := filterEnv(os.Environ(),
    "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
    "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES", "GIT_PREFIX")
env = append(env, "LC_ALL=C", "GIT_TERMINAL_PROMPT="+promptPolicy)
```

- `--git-dir` 永远是绝对路径；`cwd` 永远是项目根（`hash-object --stdin-paths` 的相对路径解析依赖 cwd）。
- 索引操作额外注入 `GIT_INDEX_FILE=<abs tmp>`；**不**注入 `GIT_WORK_TREE`（避免任何命令试图读工作树）。
- `LC_ALL=C` 让 stderr/porcelain 可稳定匹配；用户自己的 `LANG` 只影响我们主动转发的提示。
- 认证：默认继承 `GIT_ASKPASS`/`SSH_ASKPASS`/SSH agent；`--no-prompt` 时设 `GIT_TERMINAL_PROMPT=0` 并在 stderr 提示配置 credential helper。
- 错误分类：exit 128 + stderr 含 `could not read Username|Permission denied|Host key verification failed|couldn't find remote ref|unable to access` → 网络/认证（退出 4）；`stale info`/`non-fast-forward`/`failed to push some refs` → 竞争（重试）；其余 → 5。

### Object import (`hash-object`)

```go
// 一次批量：路径按字典序稳定排序，保证 OID 与文件的对应可复现
in := strings.Join(lineSafePaths, "\n") + "\n"
out := store.RunWithStdin(in, "hash-object", "-w", "--no-filters", "--stdin-paths")
// out 每行一个 40-hex，顺序与输入一致
```

- 先按 `max_bytes` 过滤并统计；超限文件**拒绝**（退出 5）或 `--allow-large` 放行，绝不静默跳过。
- 含 `\n` 的路径（POSIX 合法）不进入批量流；对这类文件单独调用 `hash-object -w --no-filters -- <path>`（`--` 之后可安全传任意路径），结果插回映射。
- `status` 用不带 `-w` 的同一命令，避免为只读操作写对象；OID 完全相同（内容寻址）。
- 空文件、二进制、UTF-16 都按字节处理；不做换行归一。

### Tree assembly (`internal/trees`)

**项目子树**（`L` 为扫描结果 `map[relPath]Entry`）：

```
GIT_INDEX_FILE=tmp/idx-sub
git --git-dir=store.git read-tree --empty
# stdin (-z)：
#   100644 <oid>\t<relPath>\0      （普通文件）
#   100755 <oid>\t<relPath>\0      （可执行位）
git --git-dir=store.git update-index -z --index-info
git --git-dir=store.git write-tree            → Tsub
```

- 不写 `<pid>/` 前缀：子树内路径就是从 docs 根算起的相对路径，提交后天然落在 `<pid>/` 下。
- 用 `read-tree --empty` 整棵重建，而不是在旧索引上删改：`update-index --add` 在 file/dir 冲突时会**静默替换**（实测），整棵重建让结果只依赖输入映射。
- 空目录不会被表示（git 不存空目录），与文档语义一致；pull 时删除后清空空目录。

**根树拼接**（只替换一个条目）：

```
git --git-dir=store.git ls-tree -z <REMOTE>            # 非递归，根层条目
  → 保留所有 name != <pid> 的行（含 README、其它项目、gitlink，原样透传）
  → 追加/替换：040000 tree <Tsub>\t<pid>
  → 若 Tsub 是空树：直接删除 <pid> 条目
printf '<entries>\0...' | git --git-dir=store.git mktree -z   → Troot
```

- 其它项目的 tree OID 原样保留，字节级不动，天然满足“只更新本前缀”。
- `mktree` 会排序并校验对象存在；不需要预排序，也不受 `core.*` 过滤影响（纯 tree 对象）。
- 远端未出生（无 commit）：条目列表只有 `<pid>` 一行。
- 为什么不 `read-tree <REMOTE>` + `read-tree --prefix`：`--prefix` 文档明示“refuse to overwrite entries that already existed”，旧条目不会被清除，必须另行删除；而 `update-index --force-remove` 需要 `GIT_WORK_TREE`（实测报 “this operation must be run in a work tree”）。splice 路线少两类边界条件，且不随远端总文件数变慢。
- 备选方案“从远端 HEAD 播种索引”确实存在（`GIT_INDEX_FILE=<tmp> git read-tree <REMOTE>`，随后用 `update-index -z --index-info` 以 `mode 0` 删掉 `<pid>/` 全部旧条目，再批量加入新条目，最后 `write-tree`）。它能工作，但需要一次全树遍历 + 一次全量删除，还要自己防 D/F 冲突；本设计不采用，只在需要“整棵树重写”（如未来导入/迁移）时使用。

### Commit and message

```
git --git-dir=store.git commit-tree <Troot> -p <REMOTE> -m <msg>   # 根提交不传 -p
```

消息模板（便于 `log` 与审计）：

```
docs(<pid>): sync from <machine>

files: +3 ~5 -1 (9 tracked)
host: <machine>
tool: doc-connector <version>

Doc-Connector-Project: <pid>
Doc-Connector-Machine: <machine>
Doc-Connector-Files: +3 ~5 -1
```

身份：默认用合成身份 `doc-connector <doc-connector@<machine>>`（不依赖用户 git 配置，跨机可复现），通过 `GIT_AUTHOR_NAME/EMAIL`、`GIT_COMMITTER_NAME/EMAIL` 注入；`config.author` 可覆盖 [G-git-env]。时间戳用当前时间。

### Fetch / push / branch

- **分支发现**只在 `init` 发生一次，之后以 `config.branch` 为准；`pull/status` 用 glob refspec 取回全部 heads，再读 `refs/remotes/origin/<branch>`。远端改名或换默认分支不会静默改变同步目标。
- **空远端**：`fetch` glob 成功但无 ref；`ls-remote` 无输出（`--exit-code` 时 2）。首推用 `--force-with-lease=refs/heads/<b>:`（空 expect），语义是“远端必须还没有这个分支”。
- **push 目标**永远是 `<sha>:refs/heads/<branch>`，不用本地分支名：镜像里没有“当前分支”，也避免 `push.default` 配置影响。
- **为什么统一用 lease 而不是普通 push**：普通 push 只保证“快进”。由于我们推送的 `<pid>` 子树完全由本地 `L` 生成，如果远端在我们 fetch 之后修改了本前缀里的文件，普通 push 仍会快进成功，并**静默覆盖那次远端修改**（git 层面不存在冲突——我们的提交是远端 tip 的后代）。lease 把“我基于哪个远端值构建”变成条件更新：远端一动就失败，强制我们重新分类，冲突检测因此覆盖所有远端改动。空远端上普通 push 也能成功（会新建分支），但为了所有路径同构，仍用 `--force-with-lease=refs/heads/<b>:`。
- **远端 HEAD 的坑**：`git init --bare` 的 HEAD 仍可能指向 `master`（Git 3.0 才默认 `main`）；我们把 `<sha>` 推到 `refs/heads/<branch>` 并不会改动远端 HEAD。若远端原本为空且默认分支是别的名字，克隆该仓库的人会看到 “remote HEAD refers to nonexistent ref”。`init` 因此先 `ls-remote --symref` 取 HEAD → 分支名，空远端则用 `--branch`/`main`，并在输出里提示用户在托管平台设置默认分支；我们自身此后只读 `refs/remotes/origin/<branch>`，不依赖远端 HEAD。
- **非快进**：普通 push 会因“必须快进”被拒（远端 tip 不是新提交祖先）；我们永远在刚 fetch 到的 tip 上建提交，所以正常情况下是快进；竞争时 lease 先失败（`stale info`），进入重试。

### Unborn remote and multiple roots

| 情形 | 检测 | 行为 |
|---|---|---|
| 远端仓库存在但空 | `ls-remote --exit-code` → 2 | `branch = --branch` 或 `main`；base=nil；首推用空 expect |
| 远端无该分支但有其它分支 | glob fetch 后 `rev-parse --verify` 失败 | 视为“本前缀尚不存在”，base=nil；不自动改分支名 |
| 本地 HEAD 未出生（无提交） | `rev-list --max-parents=0 HEAD` 为空 | 无 `--project-id` → 退出 2；有则允许继续（根提交记为 `""`） |
| 多个根提交（merge unrelated histories） | `rev-list --max-parents=0` 多行 | 拒绝（退出 2）并要求显式 `--project-id`；不取“最小者”（任取一个都是猜测，且不同实现可能选不同根） |
| 浅克隆 | `rev-parse --is-shallow-repository` = true | 无 `--project-id` → 退出 2（不能从浅历史推导真实根提交） |
| linked worktree | `abs(--git-dir) != abs(--git-common-dir)` | 退出 3（state/config 在公共目录，两个工作树会互相覆盖） |

### Reading blobs for pull (`cat-file --batch`)

```go
cmd := store.Cmd("cat-file", "--batch")     // stdin: "<oid>\n" 逐条；stdout 帧可预测
// 帧： "<oid> <type> <size>\n" + <size 字节内容> + "\n"
// 逐帧 io.CopyN 到 tmp 文件 → f.Sync()；不整块读入内存
```

单文件回退：`cat-file blob <rev>:<path>`，用于 `restore` 与冲突副本（一次一个文件，简单可靠）。两者都不触发过滤器，取到的是原始字节。

### Version gate and 2.30 gaps

```
git version  → 解析 major.minor；< 2.30 → 退出 2 并给出安装提示
```

已知在 2.30 不可用、因此**不得**使用的特性：

| 特性 | 引入版本 | 替代 |
|---|---|---|
| `rev-parse --path-format=(absolute\|relative)` | 2.31 [G-2.31] | 自己判断相对路径并用 `--show-toplevel` 拼接 |
| `check-ignore --no-index` | 晚于 2.31（2.30 与 2.31 的文档页均无此选项） | 先 `ls-files` 确认未跟踪，再用 `check-ignore -q`（未跟踪时语义等价） |
| 依赖 `init.defaultBranch` 的默认分支名 | 无（默认随版本变；Git 3.0 才改为 `main`） | 显式 `git init --bare -b` |

---

## Three-Way Algorithm

### Inputs and equality

```
B = baseline map  (state.synced_commit:<pid>, 缺失 ⇒ 空)
L = local map     (Scan(private-docs/)；忽略项已剔除)
R = remote map    (REMOTE:<pid>；缺失 ⇒ 空)

type Entry struct { Mode uint32; OID string }   // 100644 / 100755；symlink/gitlink 不进入 L
func eq(a, b *Entry) bool  // 同为 nil，或 Mode 与 OID 都相同
```

`Mode` 参与比较：`chmod +x` 被视为修改（否则 pull 会悄悄丢可执行位）。Windows 例外：扫描后、分类前做一次归一化——对 `OID` 相同而 Mode 不同的路径，若 `GOOS == "windows"`，把本地 Mode 改成基线（无基线时用远端）的 Mode，从而不因无法表达 exec 位而每次抖动。该归一化是 `sync` 在 `Classify` 前的纯内存步骤，不写文件。

### Classification table

| # | B | L | R | 类 | status | push 动作 | pull 动作 |
|---|---|---|---|---|---|---|---|
| 1 | – | – | – | 不存在 | | | |
| 2 | – | L | – | local-add | `A` | 加入子树 | — |
| 3 | – | – | R | remote-add | `A` | — | 写文件 |
| 4 | – | L | R (L≠R) | add/add | `C` | 拒绝该路径（`--resolve` 例外） | 写 `.theirs`，本地不动 |
| 5 | – | L | R (L=R) | converged | `=` | — | —（可推进基线） |
| 6 | B | B | B | unchanged | | | |
| 7 | B | L≠B | B | local-mod | `M` | 覆盖子树条目 | — |
| 8 | B | B | R≠B | remote-mod | `M` | — | 写文件 |
| 9 | B | L≠B | R≠B (L≠R) | both-modified | `C` | 拒绝 | 写 `.theirs`，本地不动 |
| 10 | B | L≠B | R≠B (L=R) | both-same | `=` | — | —（可推进基线） |
| 11 | B | – | B | local-delete | `D` | 从子树移除 | — |
| 12 | B | – | R≠B | delete/modify | `C` | 拒绝（不得复活远端改动） | 本地保持删除；无副本可写；记 unresolved |
| 13 | B | B | – | remote-delete | `D` | — | 删除本地文件 |
| 14 | B | L≠B | – | modify/delete | `C` | 拒绝 | 保留本地文件；记 unresolved（远端已删） |
| 15 | B | – | – | both-deleted | | — | —（可推进基线） |

`L≠B`/`R≠B` 均指 `!eq`，包含“模式变化”。

**push 能走哪些行**：push 用 `clobber = { p | !eq(R[p],B[p]) && !eq(L[p],R[p]) }` 判定，`clobber` 为空才继续。于是它允许 `#2 #5 #6 #7 #10 #11 #15`（本地改动，或双方已收敛到同一内容），并在出现 `#3 #4 #8 #9 #12 #13 #14`（远端有本地没有的版本）时整体拒绝（退出 1），要求先 `pull`。完整保留 15 行，是因为 `status` 与 `pull` 需要覆盖全部组合；它们与 push 共用同一个 `Classify`。

### Push actions

- push 的子树 = `L` 的全部条目（push 是“把我的目录整体声明为最新”，不做合并）。安全性由 `clobber` 集合保证：所有“远端有、本地没有”的版本都会让 push 在构建树之前中止。
- `adoption: local` 的首推（base=nil 且远端前缀非空）跳过 `clobber` 检查，直接以 `L` 替换整个 `<pid>` 子树——这是一次显式覆盖，必须由用户在 init 时选择。
- 无变化判定：`Troot == tree(REMOTE)` → 不建提交、不推送、退出 0。

### Pull actions

- 只有 `#3 #8 #13` 会改动本地文件系统；`#4 #9 #12 #14` 只增加冲突信息。
- 所有写入/删除的目标路径必须通过 Safety 的校验后才进入 journal；任一失败 → 整次 apply 在开始前中止（不写一半）。
- 目录清理：删除文件后自底向上 `Remove`（仅当为空）到 docs 根为止，不跨出 docs 根。

### Conflict artifacts

命名：`<path>.theirs.<machine>`。`machine` 来自 `config.machine`（默认 `hostname`）：转小写 → 非 `[a-z0-9]` 折叠为 `-` → 去掉首尾 `-` → 截断 32 字符 → 空则 `unknown`。不同机器得到同一后缀时靠 `.2`/`.3` 区分（后缀冲突只影响同机重复冲突，不影响可追溯性，因为提交 trailer 仍记录完整 machine）。

| 情形 | 远端内容 | 本地 | 产物 |
|---|---|---|---|
| add/add（#4） | 有 | 保留 | `<path>.theirs.<machine>` |
| both-modified（#9） | 有 | 保留 | `<path>.theirs.<machine>` |
| delete/modify（#12，本地删、远端改） | 有 | 保持删除 | `<path>.theirs.<machine>`（放在原父目录，便于用户比较后决定是否恢复） |
| modify/delete（#14，本地改、远端删） | 无 | 保留 | 无文件产物；仅 `unresolved` 记录（远端“不存在”无法物化） |

- 已存在同名产物 → 追加 `.2`、`.3`…（稳定、可预测）。
- 产物路径超过 `max_path_chars` → 改写到 `.doc-connector-conflicts/<sha1(path)[:8]>-<base>.theirs.<machine>`；该目录同样在忽略列表内。
- 产物永不同步：内置忽略 `*.theirs.*` 与 `.doc-connector-conflicts/` 同时作用于 scan 与 tree builder。
- 每次冲突写入都 `fsync` + `rename`，并在 `state.unresolved` 记录 `theirs` 路径与 `remote_oid`，使 `status` 能显示 `C path -> artifact`。

### Resolution lifecycle

```
pull 产生冲突 → unresolved=[p] ；退出 1
  ├─ 用户手工合并（编辑 p，删除 p.theirs.*）
  │     push（无 --resolve）仍拒绝 → 用户必须显式 `push --resolve p`
  │     --resolve 仅接受当前 unresolved 中的路径；清除该条后把本地版本推送
  └─ 用户接受远端
        restore p --from <REMOTE> --force     # 远端字节落到本地
        若恢复后 oid == 远端 oid → 自动清除 unresolved[p]
        然后 pull（无动作，推进基线）或 push（无变化）
```

`unresolved` 是 push 的硬闸门：只要非空，push 拒绝（退出 1），从而杜绝“冲突未看就覆盖远端”。

### Worked example

```
B: notes/a.md = oidA, notes/b.md = oidB, notes/c.md = oidC
L: notes/a.md = oidA, notes/b.md = oidB', notes/c.md 删除
R: notes/a.md = oidA', notes/b.md = oidB, notes/c.md = oidC

→ #8 remote-mod a.md   : pull 写 a.md
→ #7 local-mod b.md    : 本地改动，pull 不碰
→ #11 local-delete c.md: 本地删除，pull 不碰
```

顺序是 **先 pull 再 push**：pull 应用 a.md 后，本地 `L` 变成 `{a=oidA', b=oidB', c=缺失}`，基线推进到 REMOTE；此时再 push，`R == B` 成立，`Tsub` 由 `L` 生成并替换整个前缀——远端只保留 b' 与 c 的删除结果。分类器保证 pull 不会碰 b/c、push 不会碰它没看过的 a。

---

## Safety

### Path validation pipeline (any path entering or leaving the tree)

规则 **1–7 是致命的**：任一失败 → 该次操作整体拒绝（退出 3），错误里给出具体规则与路径。规则 8–9 是扫描期的降级处理（跳过/警告），但“写目标本身是 symlink”仍然致命。

1. **语法**：`filepath.IsLocal(p)` 为真（拒绝绝对路径、空串、含 `..` 的路径；Windows 上同时拒绝保留名）[GO-filepath]。
2. **分隔符**：拒绝 `\`（POSIX 允许，但会让跨平台路径含义分歧），拒绝连续 `/`、结尾 `/`、`.`/`..` 组件。
3. **控制字符**：拒绝 `0x00`–`0x1f`、`0x7f`；拒绝空组件。
4. **Windows 兼容**（对所有平台生效，因为仓库是跨机共享的）：拒绝 `< > : " | ? *`；拒绝组件结尾是空格或句点；拒绝保留设备名 `CON PRN AUX NUL COM1..9 COM¹²³ LPT1..9 LPT¹²³`（含带扩展名的形式，如 `NUL.txt`）[MS-naming]。
5. **保留目录**：拒绝任何组件为 `.git`；拒绝与 `.doc-connector-conflicts` 同名的一级目录（保留给冲突回退）。
6. **长度**：单组件 ≤ 255 字节；完整相对路径（含 `private-docs/` 前缀）≤ `limits.max_path_chars`（默认 240，为 Windows `MAX_PATH=260` 留出余量）[MS-maxpath]。
7. **大小写与规范化**：比较键 = NFC(Unicode 小写(p))；同一批路径中键重复 → 冲突（见下）。
8. **符号链接**：扫描时 `Lstat` 结果是 symlink → 跳过并 warn（v1 明确不跟随、不同步 symlink 本身，不中断本次操作）；pull/restore 写入前，若目标或任一父级是 symlink → 致命（退出 3）。
9. **硬链接**：`nlink > 1` 的普通文件按普通文件同步，但 `--verbose` 下 warn（硬链接指向 docs 之外时，内容会被带出；`os.Root` 也无法阻止，属用户环境问题）[GO-root]。

写入统一经 `os.Root`：

```go
r, _ := os.OpenRoot(docsDir)          // 只允许 root 内的相对路径
f, _ := r.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
// 落盘后：r.Rename(tmpRel, rel)
```

`os.Root` 的方法会拒绝逃出 root 的路径，且跟随 root 内符号链接但拒绝指向 root 外的链接（Go 1.25 起提供 `Rename`/`WriteFile`/`MkdirAll`/`RemoveAll`）[GO-root][GO-1.25]。注意：`Root` 是**路径约束**，不是安全沙箱（不拦截 bind mount、/proc、设备文件、硬链接）；v1 不试图解决这些，只保证“不越出 docs 目录、不写团队仓库”。

### Symlink policy

| 位置 | 行为 |
|---|---|
| docs 内 symlink（文件或目录） | 扫描跳过并 warn；`status` 列为 `S`（skipped），不进入 plan |
| docs 根本身是 symlink | 退出 3（FEATURES：docs 必须是真实目录） |
| 目标文件/父目录在 pull 时被换成 symlink | 退出 3（`Root` 报错 + 显式 `Lstat` 复查） |
| 远端 tree 含 mode `120000` | 视为非法输入：整次操作中止（退出 3），不写入任何路径；说明远端被外部工具污染 |

### Case collisions

同一批路径（本地扫描或远端 tree）按 `NFC(strings.ToLower(p))` 建集合：

- 本地出现两个键相同 → 退出 3（在大小写不敏感文件系统上必然互相覆盖）。
- pull 时远端路径与本地已有路径仅大小写不同 → 退出 3，**整次 apply 在写入前中止**（不落地任何文件、不写 state），错误信息列出冲突的两个路径；不会出现“应用了一半”的状态。
- macOS 的 NFD/NFC 差异在 v1 只做检测与警告（NFC 化仅用于比较键）；完整的规范化改写属于 v1.x（FEATURES 已如此安排）。

### Windows reserved names & length

见上表第 4、6 条。长度检查在**构造远端路径时**（push 前）与**落盘前**（pull/restore）都执行，因为同一棵树可能先在 Linux 上创建、后在 Windows 上检出。

### Atomic writes and fsync

- 目标文件：写 `.<base>.tmp-<rand>`（同目录，保证 rename 原子）→ `Sync` → `Rename` → 尽力目录 `Sync`。
- 目标已存在时不直接覆盖：先写临时文件，再替换（`Root.Rename` 覆盖语义）。这样崩溃点只可能留下“旧的完整文件”或“新的完整文件”。
- 不保留 mtime：git 不存 mtime，pull 后 mtime 为当前时间；文档中说明，不用 mtime 做变更检测。

### Crash recovery

| 崩溃点 | 磁盘状态 | 下次启动 |
|---|---|---|
| push：对象已写、未建 ref | 孤立 blob/tree/commit | 无影响；`gc --auto` 回收 |
| push：`pending` 已写、未 push | 有 pending ref | 视为可回收的未发布提交；下次 push 重建并覆盖 pending |
| push：push 成功、state 未写 | 远端已前进，base 旧 | 下次 fetch 发现 `REMOTE != base`；分类结果为 converged/remote-mod，不丢数据；push 会被要求先 pull 或直接重算 |
| pull：staged 已写、journal 未写 | tmp 残留 | 清理 `tmp/op-*`（无 journal 引用即删） |
| pull：journal 已写、未 apply | journal + staged | 前滚恢复 |
| pull：部分 apply | journal + 部分目标已改 | 前滚恢复（幂等）；旧内容可从基线 blob 重建 |
| pull：apply 完成、state 未写 | 目标新、base 旧 | 恢复时从 `state_next` 写 state |
| state 写入中断 | 旧 state 或新 state（rename 原子） | 以提交派生基线校验；不一致则 warn 并重算 |

`pull` 的旧内容为什么总能重建：apply 只触碰 `#3 #8 #13`，即 `L == B` 的路径；这些内容的 blob 一定在 `store.git`（来自基线提交）。因此“回滚”= 从基线重新 `cat-file`，不需要额外备份目录。

### Size guard

- 扫描时 `stat` 大小：`> warn_bytes`（50 MiB）→ 每次命令 warn；`> max_bytes`（100 MiB）→ 拒绝该文件并退出 5，除非 `--allow-large`。绝不静默跳过（FEATURES 契约）。
- 单文件读取走流式（`io.CopyN`），不整块进内存；hash 由 git 子进程完成，Go 侧不持有内容。

### Ignore filter (exact rules)

内置列表按**basename**匹配（大小写不敏感，仅对文件）：`.DS_Store`、`._*`、`Thumbs.db`、`ehthumbs.db`、`desktop.ini`。按**路径**匹配：`.git/`（任何层级，防御性）、`*.theirs.*`（任意 `theirs` 后缀，含 `.theirs.<machine>.2`）、`.doc-connector-conflicts/`（一级目录）。`config.ignore.extra` 追加同样的 basename/glob 规则。

- 过滤同时作用于 `L`（本地扫描）与 `R`（远端子树）：被忽略的远端路径对 classification 不可见，因此不会被拉回本地；下一次 push 用 `L` 覆盖整个 `<pid>` 子树时会把它们自然移除（忽略列表是内置且跨机一致的，不会误删别人的正常文件）。
- 过滤发生在 `stat`/hash 之前；被忽略的路径既不进入 `L`，也不会被 pull 删除或创建。
- **不读取 docs 目录内的 `.gitignore`**：那份文件本身会被同步，若再参与过滤会造成“同一仓库在不同机器上同步集合不同”。`.docignore`（gitignore 语法）是 v1.x，且必须显式解析、固定语法子集。
- 被忽略路径的统计写入 `status` 的末行（`ignored: N`），便于用户确认不是“文件丢了”。

---

## Concurrency

| 竞争 | 机制 | 结果 |
|---|---|---|
| 同一项目两个 doc-connector 进程 | `.git/doc-connector/lock`（O_EXCL + PID/host 存活检测；Unix 可加 `flock`） | 第二个进程退出 3，打印持有者 |
| 同一 store 的 ref 更新 | `update-ref <ref> <new> <old>` 的 CAS + git 自身 ref 锁 | 冲突方失败并重试/报错 |
| 两个项目推同一远端 | 各自 store + `--force-with-lease` + fetch/重建/重试（≤3 次，100/300/900ms + jitter） | `clobber` 为空（对方只改了别的项目目录，或远端已收敛）→ 在最新 tip 上重建并推成线性历史；否则退出 1 要求 pull |
| 同一项目两台机器 | 同上；后推者 lease 失败 → fetch → 比较本前缀 | 本前缀有远端改动 → 退出 1 要求 pull（pull 在无同路径冲突时自动叠加）；同路径改动 → 冲突副本 |
| 外部 `git push` 改写了远端历史（非追加） | lease 仍会成功（值是刚 fetch 的），但 base 不再可达 | `rev-list`/`merge-base` 检测 base 不是 REMOTE 祖先 → 退出 1，要求显式 `--adopt-local` 或人工处理；绝不静默重写 |
| 网络中断在 push 响应丢失时 | 下次 fetch 发现远端已有我们的提交（`C` 是 REMOTE 的祖先或相等） | 视为成功：`synced_commit = C`，不重复提交 |

重试的实现约束：

- 每次重试必须**重新 fetch**，不能复用上次的 `REMOTE`（否则 lease 期望值过期，会一直 `stale info`）。
- 重试期间本地文件可能被用户改动：重试前重新 `Scan` 或校验 `L` 的 OID 集合未变；变了就中止（退出 5），避免推送用户没看过的内容。
- 重试耗尽 → 退出 4，消息里包含最后一条 porcelain 行与建议命令。

---

## Build Order and Dependency Graph

```
(0) version + gitstore/exec          ── 一切的基础；用假 git 可测 argv/env/错误分类
        │
(1) project (root/identity)          ── 纯函数 + rev-parse；表驱动测多根/浅克隆
        │
(2) config + state + atomic + lock   ── 崩溃注入测试（写一半、rename 前）
        │
(3) ignore + fsx/validate + scan     ── 纯路径规则表；symlink/大小写/保留名用例
        │
(4) trees (build subtree + splice)   ── 对真实 git 断言 tree OID；含空树/删除整个前缀
        │
(5) gitstore (fetch/push/refs/blob)  ── 本地 file:// 远端做集成测试；lease 拒绝路径
        │
(6) init (guard + exclude + adoption)── 幂等、被跟踪目录拒绝、linked worktree 拒绝
        │
(7) sync/classify + status           ── 15 种状态全覆盖；不写盘即可验收
        │
(8) pull/apply + journal + conflicts ── kill -9 注入；artifact 命名/去重
        │
(9) push + retry                     ── 两 store 竞争模拟；无变化 no-op
        │
(10) log + restore                   ── 路径含空格/冒号；删除后恢复；--force 语义
        │
(11) 跨平台收尾（Windows 路径/模式/换行） + `gc --auto`
```

依赖关系（箭头 = 需要先完成）：

```
exec ─► project ─► config/state ─► fsx/ignore/scan ─► trees ─► gitstore
                                                          │        │
                                                          └──► init ◄┘
                                                                 │
                                                     classify ───┤
                                                          │       │
                                                        pull ──► push
                                                          │
                                                     log/restore
```

关键可测缝隙：

- `Classify` 是纯函数 → 15 行表的单元测试，不需要磁盘。
- `gitstore` 接口化（`Runner`）→ 其余组件用回放样本测试，只有 `gitstore` 自己跑真实 git。
- `Apply` 接受 `Plan` 与 `fsx.Root` 接口 → 用内存 FS 测崩溃点，用真实 `os.Root` 测路径逃逸。

---

## Scaling Considerations

| 规模 | 影响 | 架构调整 |
|---|---|---|
| 10–500 文件 / < 50 MB | 单次 push < 1s（本地 git），瓶颈是网络 | 无需调整 |
| 500–5k 文件 / 100 MB | `hash-object --stdin-paths` 一次进程；`ls-tree -r` 输出数 MB | 批量 OID 解析；blob 读取用 `--batch` 流式；scan 可并行 stat（上限 GOMAXPROCS） |
| 5k–50k 文件 | 全量 hash 每次数秒；JSON state 变大（仅 unresolved 与几个 OID，仍小） | 引入 mtime/size 快速路径（OID 仍由 git 校验），`status` 默认只 hash 可疑文件 |
| 远端含多个大项目 | splice 只读根层条目，成本与本项目无关；但 `cat-file`/fetch 受远端总大小影响 | 不采用 shallow（restore 需要历史）；接受全量镜像，靠增量 fetch |
| 提交数 10k+ | `log` 需分页 | `log --max-count`、`--since`；镜像 `gc --auto` |

**First bottleneck:** 本地全量 hash（每次命令 O(文件数×大小)）。修法：size+mtime 缓存（存 `state.scan_cache`，仅作加速，OID 仍是权威），而不是引入 watch。
**Second bottleneck:** 远端 fetch 的全历史下载。修法：镜像已增量 fetch；首次克隆成本不可避免（文档规模决定）。

---

## Anti-Patterns

### Anti-Pattern 1: 在临时目录 `git clone`/`checkout` 远端再 `add`

**What people do:** 为了用 porcelain（`git add -A`、`git commit`），把远端检出到临时工作树。
**Why it's wrong:** 需要处理 `.gitattributes`/`core.autocrlf` 的字节改写、空目录、符号链接、权限位；临时工作树与真实 docs 目录会产生“谁是最新”的漂移；崩溃后残留工作树；Windows 上文件锁/长路径问题成倍。
**Do this instead:** plumbing 组合（`hash-object` → index → `write-tree` → `mktree` → `commit-tree`），全程无工作树。

### Anti-Pattern 2: 用团队仓库的 index 做暂存

**What people do:** 设置 `GIT_INDEX_FILE` 为空或复用 `.git/index`，然后 `git add private-docs`。
**Why it's wrong:** 一次误操作就把私有文档写进团队 index，下一次 `git commit -a` 即泄露；`.git/index` 的 stat 缓存被污染也会影响团队仓库性能与 `git status`。
**Do this instead:** 永远显式 `GIT_INDEX_FILE=<tmp>` 且 `--git-dir=store.git`；环境里剥掉继承的 `GIT_*`。

### Anti-Pattern 3: 在旧索引上 `update-index --add` 增量更新

**What people do:** `read-tree <remote>` 后直接加自己的条目，指望得到新树。
**Why it's wrong:** 已存在条目不会被覆盖清理；`--add` 遇到 file/dir 冲突会**静默替换**（实测），可能丢掉别的项目条目；`read-tree --prefix` 明确拒绝覆盖旧条目。
**Do this instead:** 子树 `read-tree --empty` 整棵重建；根层用 `ls-tree`+`mktree` 只替换一个条目。

### Anti-Pattern 4: `push --force` 或“先 push 失败再强推”

**What people do:** 遇到非快进就 `--force`。
**Why it's wrong:** 会抹掉另一台机器刚同步的提交，且与“绝不丢改动”直接冲突；`restore` 的审计链断裂。
**Do this instead:** `--force-with-lease=<ref>:<expect>`，失败即 fetch→比较本前缀→重建→重试；本前缀被远端改过就要求先 pull。

### Anti-Pattern 5: 用 mtime/大小判断变更

**What people do:** 缓存 (mtime,size) 作为“是否修改”的判据。
**Why it's wrong:** `touch`、复制、checkout 造成假变更；同大小同秒内的编辑被漏掉——正是用户列出的“不知道是否最新”的根源。
**Do this instead:** 以 blob OID 为准；缓存只能加速，不能替代。

### Anti-Pattern 6: 冲突时“自动合并文本”

**What people do:** 对文本调用 `git merge-file` 或行级合并。
**Why it's wrong:** 散文合并会产生语义错误但语法合法的文档；PDF/Office 无法行合并；静默合并正是用户最不能接受的失败。
**Do this instead:** 保留本地 + `.theirs.<machine>`，显式 `push --resolve` / `restore --force`。

### Anti-Pattern 7: 把镜像当成“第二个工作副本”

**What people do:** 在 `store.git` 里建分支、checkout、手工提交。
**Why it's wrong:** 镜像的 ref 语义被我们严格定义（`refs/remotes/origin/*` 是远端观测值，`pending` 是未发布提交）；人工干预会让 base 推导与 lease 期望失效。
**Do this instead:** 只通过 CLI 写入；需要人工检查时用 `git --git-dir=… log/show` 只读命令。

### Anti-Pattern 8: 先写 state 再写文件

**What people do:** 更新 `synced_commit` 后再 apply 文件。
**Why it's wrong:** 崩溃后基线声称“已同步”，实际文件是旧的，下一次 push 会把旧内容当成新改动推上去（或把远端新内容判成已收敛）。
**Do this instead:** journal → 文件 → state → 删 journal；state 是最后一步，且可前滚恢复。

---

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---|---|---|
| system `git` (≥2.30) | `exec.Command` 直接调用，不用 libgit2/go-git | 复用 SSH agent、credential helper、proxy、`insteadOf`；版本门禁在启动时一次；`LC_ALL=C` 稳定解析 |
| 私有远端（GitHub/Bitbucket/自建/`file://`） | `fetch`/`push` 显式 URL + 分支 refspec | 不自动建仓；不内嵌凭据；空仓库是合法初始状态；远端可改写历史 → 检测 base 非祖先 |
| SSH agent / credential helper | 完全交给 git；`GIT_TERMINAL_PROMPT` 可关 | 非交互模式下认证失败要给出明确提示（退出 4），不要挂起 |
| 文件系统（ext4/APFS/NTFS/9p） | `os.Root` + 原子 rename | WSL 的 9p 对大目录 stat 慢；rename 跨设备（EXDEV）→ 复制到目标目录临时文件再 rename |

### Internal Boundaries

| Boundary | Communication | Notes |
|---|---|---|
| `cli ↔ app` | `Request`/`Result` 结构体 + `ExitCode` | cli 不做 I/O，app 不打印 |
| `app ↔ sync` | `Plan` in / `ApplyResult` out | `--dry-run` 只取 `Plan` |
| `sync ↔ gitstore` | 方法调用（非接口泄漏 argv） | `TreeOf/Fetch/Push/Commit/ReadBlob` 是稳定契约；push 结果用枚举而非字符串 |
| `sync ↔ fsx` | `Root` 接口 + `PathPolicy` | 所有路径先过 `Validate` 再 `Open` |
| `trees ↔ gitstore` | 只通过 index 与对象命令 | 树构建不直接读文件内容（blob 已由 scan 导入） |
| `state ↔ 全体` | 只在命令首尾读写 | 命令中间不写 state；journal 由 apply 独占 |

**`gitstore` 边界契约**（其它包只依赖这些方法）：

```go
type Repo struct { GitDir string; WorkDir string }       // GitDir 绝对；WorkDir = 项目根
func (r *Repo) TreeOf(commit, prefix string) (map[string]Entry, error)
    // commit=="" → 空 map；先 cat-file -t <commit>:<prefix> 必须为 tree（blob 视为损坏）
    // ls-tree -r -z <commit>:<prefix>，去掉前缀
func (r *Repo) Splice(remoteCommit, prefix string, sub map[string]Entry) (treeOID string, err error)
func (r *Repo) ImportBlobs(paths []string, write bool) (map[string]string, error)
func (r *Repo) Commit(tree string, parents []string, msg string, ident Identity) (string, error)
func (r *Repo) Push(url, branch, commit, expect string) (PushResult, error)
func (r *Repo) Fetch(url string) (commit string, err error)   // "" = 远端无该分支
func (r *Repo) ReadBlobFile(rev, path string, w io.Writer) error
func (r *Repo) IsAncestor(a, b string) (bool, error)
```

`TreeOf` 的 `cat-file -t` 预检很重要：远端若出现 `<pid>` 是 blob（外部工具污染），后续 `ls-tree` 会给出难以理解的错误；提前判型能给出“远端布局异常”的明确信息。

**Push 结果枚举**（避免解析人类输出）：

```go
type PushClass int
const (
    PushOK PushClass = iota
    PushLeaseRejected      // "stale info"
    PushNonFastForward     // 无 lease 时的拒绝
    PushAuthFailed
    PushNetworkFailed
    PushUnknown
)
```

判定依据：exit code + porcelain 行（`!\t…\t[rejected] (stale info)` / `[rejected] (non-fast-forward)` / `[remote rejected]`）+ stderr 关键字；三者都保留在 `GitError` 里便于诊断。

---

## Sources

- [G-rev-parse] git-rev-parse — `--show-toplevel`, `--absolute-git-dir`, `--git-common-dir`, `--is-shallow-repository`: https://git-scm.com/docs/git-rev-parse
- [G-rev-list] git-rev-list — `--max-parents=0`, `--count`: https://git-scm.com/docs/git-rev-list
- [G-ls-remote] git-ls-remote — `--symref`, `--exit-code`（无匹配返回 2）: https://git-scm.com/docs/git-ls-remote
- [G-init] git-init — `-b/--initial-branch`（默认仍为 master）: https://git-scm.com/docs/git-init
- [G-fetch] git-fetch — refspec、`--no-tags`、`--prune`: https://git-scm.com/docs/git-fetch
- [G-push] git-push — `--force-with-lease=<refname>:<expect>`（空 expect = 必须不存在）、`--porcelain`: https://git-scm.com/docs/git-push
- [G-hash-object] git-hash-object — `--stdin-paths`, `--no-filters`: https://git-scm.com/docs/git-hash-object
- [G-read-tree] git-read-tree — `--empty`, `-i`, `--prefix` 拒绝覆盖既有条目: https://git-scm.com/docs/git-read-tree
- [G-update-index] git-update-index — `--index-info` 三种输入格式、`mode 0` 删除、`-z`、`--force-remove` 语义: https://git-scm.com/docs/git-update-index
- [G-write-tree] git-write-tree — https://git-scm.com/docs/git-write-tree
- [G-ls-tree] git-ls-tree — `-r`, `-z`, 输出格式: https://git-scm.com/docs/git-ls-tree
- [G-mktree] git-mktree — 输入格式、自动排序、对象存在性校验、`-z`: https://git-scm.com/docs/git-mktree
- [G-commit-tree] git-commit-tree — `-p`, 消息来自 stdin: https://git-scm.com/docs/git-commit-tree
- [G-update-ref] git-update-ref — CAS 语义、40 个 0/空串表示必须不存在: https://git-scm.com/docs/git-update-ref
- [G-cat-file] git-cat-file — `<rev>:<path>`, `--batch` 帧格式: https://git-scm.com/docs/git-cat-file
- [G-merge-base] git-merge-base — `--is-ancestor`: https://git-scm.com/docs/git-merge-base
- [G-log] git-log — `--format`, 路径过滤: https://git-scm.com/docs/git-log
- [G-gc] git-gc — `--auto`: https://git-scm.com/docs/git-gc
- [G-check-ignore] git-check-ignore — `--quiet`, 退出码 0/1；2.30/2.31 版文档无 `--no-index`（故不使用）: https://git-scm.com/docs/git-check-ignore
- [G-check-ref-format] git-check-ref-format — `--branch` 校验分支名（合法 0，非法非 0）: https://git-scm.com/docs/git-check-ref-format
- [G-gitignore] gitignore(5) — `$GIT_COMMON_DIR/info/exclude` 的优先级: https://git-scm.com/docs/gitignore
- [G-config] git-config — `safe.bareRepository`（Git 3.0 默认 `explicit`）: https://git-scm.com/docs/git-config
- [G-git-env] git(1) — `GIT_INDEX_FILE`、`GIT_AUTHOR_NAME/EMAIL`、`GIT_COMMITTER_*`: https://git-scm.com/docs/git
- [G-2.31] Git 2.31 release notes — `rev-parse --path-format=(absolute|relative)` 为 2.31 新增: https://raw.githubusercontent.com/git/git/master/Documentation/RelNotes/2.31.0.adoc
- [GO-root] Go `os.Root` — traversal-resistant API，`Rename/WriteFile/MkdirAll/RemoveAll` 及限制: https://pkg.go.dev/os#Root
- [GO-1.24] Go 1.24 Release Notes — 新增 `os.Root` / `os.OpenRoot`（目录受限的文件 API）: https://go.dev/doc/go1.24
- [GO-1.25] Go 1.25 Release Notes — `Root.Chmod/…/Rename/WriteFile` 新增: https://go.dev/doc/go1.25
- [GO-filepath] Go `filepath.IsLocal` — 拒绝绝对路径、`..`、Windows 保留名: https://pkg.go.dev/path/filepath#IsLocal
- [GO-rename] Go `os.Rename` — 替换语义与非 Unix 平台非原子说明: https://pkg.go.dev/os#Rename
- [MS-naming] Microsoft — Naming Files, Paths, and Namespaces（保留字符、`CON/PRN/AUX/NUL/COM1-9/LPT1-9`、结尾空格/句点、大小写不敏感）: https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file
- [MS-maxpath] Microsoft — Maximum Path Length Limitation（`MAX_PATH` = 260）: https://learn.microsoft.com/en-us/windows/win32/fileio/maximum-file-path-limitation
- 本机实测：git 2.47.3（`git version`）、go1.27.2；关键行为（glob fetch 空远端 exit 0、显式 refspec exit 128、`--index-info` 参数顺序、`mode 0` 删除、`--force-with-lease=ref: ` 拒绝、porcelain 拒绝行、`mktree` 排序与 tree 条目、`cat-file --batch` 帧、linked worktree 路径差异）均已复现。
- 项目内约束：`/mnt/c/Home/Repos/doc-connector/.planning/PROJECT.md`、`.planning/research/FEATURES.md`（退出码 0–5、`push --resolve`、`restore --force`、50/100 MB 阈值、内置忽略列表）。

---
*Architecture research for: doc-connector（Go CLI · git plumbing 同步）*
*Researched: 2026-10-22*
