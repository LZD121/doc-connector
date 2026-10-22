<!-- GSD:project-start source:PROJECT.md -->

## Project

**doc-connector**

一个 Go 单二进制 CLI，把项目里的私有文档目录映射到**个人私有 git 仓库**中的一个哈希目录，并在多台机器之间 `push` / `pull`。文档始终以普通文件形式存在于主仓库工作区的子目录中，但被 `.git/info/exclude` 排除，因此主仓库的历史、分支列表和 `git status` 永远不会出现它们。

它面向“主仓库是团队仓库、不能放私人文件”的开发者：代码走团队仓库，私人笔记与商务文档走自己的私有仓库，两条轨道互不干扰。

**Core Value:** 让私有文档在多台机器间**可验证地**同步，同时保证主仓库的历史与工作区永远不含它们。任何取舍都优先保证“不丢改动”和“不误入主仓库”。

### Constraints

- **语言/分发**：Go 单二进制，无运行时依赖；`GOOS` 交叉编译覆盖 linux/darwin/windows。
- **Git 版本**：依赖底层 plumbing，要求系统 `git >= 2.30`。
- **认证**：不实现自己的认证，shell out 到 `git`，复用 SSH agent、credential helper、代理配置。
- **历史完整性**：浅克隆无法推导真实根提交，必须报错并要求 `--project-id` 显式指定，绝不猜测。
- **主仓库不可污染**：不得写入 `.gitignore`、不得修改 index、不得创建提交。唯一的例外是 `.git/info/exclude`（本地、未跟踪）。
- **配置隐身**：配置存于 `.git/doc-connector/config.yaml`，随克隆丢失；新机器必须重新 `init`。这是用户明确选择的代价。
- **安全边界**：项目哈希是身份校验而非认证；任何能读到主仓库的人都能算出它，真正的访问控制由私有仓库 ACL 提供。

<!-- GSD:project-end -->

<!-- GSD:stack-start source:research/STACK.md -->

## Technology Stack

## Recommended Stack

### Decision summary

| Question | Decision | One-line reason |
|---|---|---|
| Go version | Build with **1.27.3**, `go 1.26.0` in `go.mod` | 1.26 is the oldest still-supported release line; two runtime deps already require `go 1.26.0` |
| CLI framework | **stdlib `flag`** + explicit subcommand switch | 6 flat commands, ~10 flags total, zero deps, exact control of help/exit codes |
| Config format | **YAML** via `go.yaml.in/yaml/v3` **v3.0.5**, struct + `,inline` map | Comments/multiline for humans; inline map round-trips unknown keys |
| Git access | **shell out to system git ≥ 2.30** | go-git has no `commit-tree` / `update-ref` / `rev-parse` / credential helpers |
| Cross-process lock | **`github.com/gofrs/flock` v0.13.1** | `flock(2)` on Unix, `LockFileEx` on Windows, context-aware retry |
| Atomic replace | **hand-rolled temp + `Rename` + fsync + Windows retry** | `os.Rename` replaces on Windows but is not atomic; no portable library exists |
| Tests | **stdlib `testing`** + **`rogpeppe/go-internal/testscript` v1.16.0** | Script-driven CLI tests against real local bare remotes |
| Release | **GoReleaser v2.18.3** + GitHub Actions | Static matrix builds, checksums, Homebrew cask, Scoop bucket |
| Logging / UX | **`log/slog`** to stderr, hand-written human renderer, `--json` on stdout | Machine output and diagnostics never share a stream |
| Path safety / ignore | **`os.Root`** + a fixed in-code junk set | `os.Root` is purpose-built for subtree-confined traversal; no glob engine needed |

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|---|---|---|---|
| Go toolchain | **1.27.3** (released 2026-10-19); `go 1.26.0` directive | Compiler / runtime | 1.27.3 is the current stable patch (includes the 2026-10-19 `crypto/x509` security fix). `go 1.26.0` is the correct *floor*: Go supports each release until two newer majors exist, so 1.25 stopped receiving fixes when 1.27 shipped. `golang.org/x/term v0.47.0` and `golang.org/x/sys v0.49.0` both declare `go 1.26.0`. |
| stdlib `flag` | stdlib | Argument parsing, 6 subcommands | Commands are flat (`init`, `push`, `pull`, `status`, `log`, `restore`), each with 0–5 flags, and only `restore` takes a positional (last). `flag` supports `-x` and `--x`; the “flags must precede positionals” limitation is therefore never exercised. Zero modules, zero behavior drift, help text written by us so `testscript` golden files stay stable. |
| `go.yaml.in/yaml/v3` | **v3.0.5** | Hidden config file `.git/doc-connector/config.yaml` | Official YAML-org continuation of `gopkg.in/yaml.v3` after its author marked the original unmaintained in April 2025. Same API, same v3 semantics. `gopkg.in/yaml.v3` is frozen at v3.0.1 (2022). |
| system `git` | **≥ 2.30**, tested 2.47.3 | All repository I/O: hashing, tree/commit construction, refs, fetch/push, diff | Reuses the user's SSH agent, credential helper, proxy, `insteadOf`, certificates, and corporate config for free. Only the CLI path gives us `commit-tree`, `update-index`, `update-ref`, `rev-parse`, `check-ignore`, and `ls-remote`. |
| `github.com/gofrs/flock` | **v0.13.1** | One-writer-at-a-time across processes | Thin, maintained, thread-safe; `TryLockContext` gives a bounded wait; kernel releases the lock if the process dies (no stale-lock recovery code). Uses `unix.Flock` and Windows `LockFileEx`. |
| `log/slog` | stdlib | Diagnostics (`-v`, `--log-format=json`) | Structured, leveled, no dependency. Used **only** for stderr diagnostics; user-facing success/failure text is hand-written. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---|---|---|---|
| `golang.org/x/term` | **v0.47.0** | `term.IsTerminal(fd)` | TTY detection for color, progress, and the `GIT_TERMINAL_PROMPT` policy. Correct on Windows consoles where a `ModeCharDevice` stat check is wrong (e.g. `NUL`). Requires `go 1.26.0`. |
| `golang.org/x/sys` | **v0.49.0** (indirect) | `LockFileEx`/console syscalls underneath `flock` and `x/term` | Pulled by MVS because `x/term v0.47.0` requires it. Keep it indirect: the atomic-write retry deliberately retries *any* rename error, so no `windows.ERROR_*` import (and no build-tagged file) is needed. |
| `github.com/rogpeppe/go-internal` | **v1.16.0** | `testscript` + `txtar` for end-to-end CLI tests | Test-only import (`_test.go`, `TestMain`). Never linked into the shipped binary. Same engine the Go team uses for `cmd/go` scripts. |

### Development Tools

| Tool | Purpose | Notes |
|---|---|---|
| GoReleaser **v2.18.3** | Cross-compile matrix, archives, checksums, changelog, package-manager publishing | Config must start with `version: 2`. `brews:` is deprecated since v2.10 — use `homebrew_casks:`. |
| `golangci/golangci-lint` **v2.15.0** | Lint (`errcheck`, `govet`, `staticcheck`, `revive`, `gosec`, `bodyclose`) | v2 requires `version: "2"` in `.golangci.yml`. Run on Linux only (fast path); the compile/test matrix covers OS-specific breakage. |
| `actions/checkout` **v7.0.1** | CI checkout | Set `fetch-depth: 0` only for the release job (GoReleaser changelog); tests need depth 1. |
| `actions/setup-go` **v7.0.0** | CI Go install + module cache | `go-version-file: go.mod` for the primary job; explicit `1.26.x` in the minimum-version matrix leg. |
| `goreleaser/goreleaser-action` **v7.3.0** | Runs GoReleaser on tag push | `permissions: contents: write`; do not use the default token for the Homebrew tap (different repo). |
| `git` **≥ 2.30** in CI | Real integration tests | Ubuntu/macOS runners ship adequate git; Windows runners ship Git for Windows (set `core.autocrlf=false` in tests). |

## Installation

# Library/runtime dependencies (all pure Go, CGO-free)

# Test-only (imported from _test.go / TestMain; never linked into the binary)

# Build locally, exactly as CI does

# End-user install from source (requires Go >= 1.26 because of the go directive)

# Release

## Stack Decisions in Detail

### 1. Go version and cross-compilation

- **Build with 1.27.3. Target `go 1.26.0`.** Do not put `toolchain go1.27.3` in `go.mod`: it forces a toolchain download for users building from source and gains nothing (the code compiles on 1.26).
- Supported build matrix: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, `windows/arm64`. All dependencies are pure Go, so `CGO_ENABLED=0` produces fully static binaries; `GOAMD64` stays at the default `v1`. Cross-compilation is not cross-testing: Windows/macOS behavior (rename semantics, path casing, git for Windows) can only be validated on the CI matrix.
- `-trimpath` for reproducible paths; `-buildvcs=false` because version/commit are stamped via `-ldflags` and a shallow CI clone otherwise makes the binary report `(modified)`.
- Platform floors inherited from the toolchain: **macOS 13 Ventura** for Go 1.27-built binaries (Go 1.27 release notes, “Darwin”), Windows 10+.
- Verify at runtime with `git version` — never at build time. Parse `versionFields[2]` and compare major/minor numerically; Apple (`2.39.3 (Apple Git-145)`) and Windows (`2.47.1.windows.1`) suffixes must not break the parse.

### 2. CLI framework: stdlib `flag` (recommended)

### 3. Config format and library

- **Never call `dec.KnownFields(true)`.** Unknown keys are captured by the inline map and re-emitted on the next write; emit a single warning listing them so typos (`remot:`) are visible.
- Round-trip fidelity is **semantic, not byte-identical**: comments, key order, anchors, and quoting style are not preserved with the struct+inline approach. If byte-stable editing is ever required, decode into `yaml.Node`, mutate the specific mapping entries, and re-encode — same library, more code. Do not add a second YAML library for this.
- Write with `yaml.NewEncoder(f); enc.SetIndent(2)`. Write atomically (see §5) and with mode **0600**, because a user may paste an HTTPS URL containing a token. Recommend SSH URLs in all messages.
- The file lives at `<absolute-git-dir>/doc-connector/config.yaml`; resolve the git dir with `git rev-parse --absolute-git-dir` (never assume `.git` is a directory — submodules and `--separate-git-dir` use a file). `--git-common-dir` is usually returned **relative** (`.git`), so resolve it against `--show-toplevel` before comparing. Refuse to run when the resolved common dir differs from `--absolute-git-dir` (linked worktree): two worktrees would otherwise race on one `<project_id>/` folder.
- Why YAML over TOML/JSON: the file is hand-edited, benefits from comments and multi-line values (remote URLs, future per-host settings), and the path `config.yaml` is already a project decision. TOML (`github.com/BurntSushi/toml` v1.6.0) is a fine second choice but buys nothing here and makes nested state (`last_synced`) wordier. JSON is for machines only.

### 4. Git integration: shell out, and here is the bill

| Variable | Value | Why |
|---|---|---|
| `GIT_TERMINAL_PROMPT` | `0` when stdin or stderr is not a TTY, or with `--json` | Prevents hangs in scripts/CI; documented as “git will not prompt on the terminal”. Leave unset in an interactive TTY so first-time HTTPS auth can prompt. |
| `LC_ALL`, `LANG` | `C` | Stable, parseable English diagnostics; git output is localized otherwise. |
| `GIT_PAGER` | `cat` (plus `--no-pager`) | A configured pager must never capture stdout. |
| `GIT_CONFIG_GLOBAL` / `GIT_CONFIG_NOSYSTEM` | **do not set** in production | They would disable credential helpers, `insteadOf`, proxies. Set them only in tests. |
| `GIT_AUTHOR_*` / `GIT_COMMITTER_*` | read `git config user.name` / `user.email` from the main repo and set all four explicitly on `commit-tree`; if missing, exit 4 with `git config --global user.name/user.email` instructions | `commit-tree` fails with “Author identity unknown” in a fresh container, and an interactive failure mid-push is worse than a precondition check. |
| Operation | Command |
|---|---|
| Repo discovery | `git -C <dir> rev-parse --show-toplevel --absolute-git-dir --git-common-dir --is-shallow-repository` (one call; output lines are positionally ordered, and `--git-common-dir` may be relative) |
| Root commit (project id) | `git rev-list --max-parents=0 HEAD` (error if >1 root or shallow) |
| Object format | `git config --get extensions.objectFormat` (empty ⇒ sha1); never assume 40-hex |
| Exclusion guard | `git -C <dir> check-ignore -q -- <docs_dir>/` (0 ignored, 1 not ignored, 128 fatal) and `git -C <dir> ls-files -z -- <docs_dir>` must be empty |
| Local file set | `fs.WalkDir(os.Root.FS(), ".")`; hash with `git --git-dir=<store> hash-object -w --no-filters --stdin-paths` run with `cmd.Dir = docsDir`, feeding `filepath.ToSlash(rel)` newline-separated (reject names containing `\n`/`\r`). `--no-filters` is mandatory: it stores bytes exactly as on disk, bypassing CRLF/eol/clean filters. |
| Empty remote detection | `git --git-dir=<store> ls-remote --heads <url>` |
| Fetch | `git --git-dir=<store> fetch --no-tags --prune <url> "+refs/heads/*:refs/remotes/docs/*"` — glob refspec, because an explicit `refs/heads/main:...` refspec **fatals** on an empty remote; the glob succeeds with zero refs |
| Compare | `git merge-base --is-ancestor A B`; `git rev-list --count A..B`; `git diff-tree --no-commit-id -r -z --no-renames A B` |
| Read tree | `git ls-tree -r -z --full-tree <commit>` (NUL-terminated `mode SP type SP sha TAB path`) |
| Read blobs | one `git cat-file --batch` process; stream `io.CopyN` per header — never buffer a whole document |
| Build tree | `GIT_INDEX_FILE=<abs tmp> git --git-dir=<store> read-tree <parent>` (or `--empty`), then one `update-index -z --index-info` stream (it reads stdin itself; do not also pass `--stdin`): **all `0 <all-zero oid> TAB path` deletions first**, then all `100644 <blob> TAB <project_id>/<path>` additions — deleting first prevents file↔directory transitions from colliding — then `write-tree` |
| Create commit | `git --git-dir=<store> commit-tree <tree> -p <parent> -F -` (message on stdin) with explicit author/committer env |
| Move local ref | `git --git-dir=<store> update-ref refs/doc-connector/local <new> <old>` (CAS) |
| Push | `git --git-dir=<store> push --porcelain <url> <sha>:refs/heads/main` — never `--force`; non-fast-forward is rejected by the remote, which is the safety net |
| History / restore | `git --git-dir=<store> log --format=... <ref> -- <project_id>/`; `restore` = read blob at `<commit>:<project_id>/<path>` via `cat-file` |
| Capability | System git (chosen) | go-git v5.19.3 | Practical consequence |
|---|---|---|---|
| Credential helpers | Runs every configured `credential.helper` | **Unsupported** (issue #1420, maintainer-confirmed) | No GCM, `osxkeychain`, `store`, or corporate helper; users must inject secrets in code |
| SSH agent | Yes, via the system `ssh` | Yes (`plumbing/transport/ssh`) | Tie — the one place go-git is equivalent |
| `GIT_TERMINAL_PROMPT` | Honored; set `0` for non-TTY and CI | No terminal interaction to disable | Interactive-first-run and CI-non-interactive both work with one env var |
| `includeIf` | Exact `gitdir`/`onbranch`/`worktree` evaluation per git-config(1) | Not git's evaluator | Per-identity/per-proxy includes behave as documented only with real git |
| `safe.directory` | Enforced, with an actionable “dubious ownership” error | No ownership enforcement | Real git refuses a repo it considers unsafe; go-git would silently read it |
| Plumbing | Everything | `commit-tree`, `read-tree`, `write-tree`, `update-index`, `update-ref`, `rev-parse`, `check-ignore` all **unsupported** | The tree/commit/ref construction in this design is impossible without reimplementing git's object model |
| Proxy / TLS trust | git's `http.proxy`, `http.sslCAInfo`, URL rewrites | Go `net/http` stack; git config knobs are not applied the same way | Corporate proxy/CA setups may require a custom transport |
- `COMPATIBILITY.md` marks `commit-tree`, `read-tree`, `write-tree`, `update-index`, `update-ref`, `rev-parse`, and `check-ignore` as **unsupported**. Those are exactly the plumbing this design is built on.
- Credential helpers are **not supported**; maintainer response on issue #1420 (2025-02-03): “this is currently a feature that `go-git` does not support”. That removes Git Credential Manager, `osxkeychain`, `store`, and enterprise helpers, i.e. the whole reason to reuse system git.
- v6 is still `v6.0.0-beta.1` (2026-10-04). v5.19.3 is the only production choice, and it also requires `go 1.26.0`.
- go-git does parse `~/.gitconfig` (local rw, global/system ro) and supports SSH agent, but it never executes credential helpers, so the authentication model diverges no matter how the config is read.

### 5. Cross-process locking and atomic file replacement

- Lock file: `<absolute-git-dir>/doc-connector/lock`, created with `flock.New(path)`. Create the `doc-connector` directory with `os.MkdirAll(dir, 0o700)` first. It is outside the docs tree and outside `store.git`, so git never sees it.
- Every command that writes (and `status`, which reads a ref while another process may push) takes the **exclusive** lock: `ok, err := fl.TryLockContext(ctx, 250*time.Millisecond)` with a 10 s `context.WithTimeout`. On timeout: exit 1 with “another doc-connector process holds `<path>`” plus the PID recorded in the file.
- The OS releases the lock on process death, so there is no stale-lock heuristic and no `os.Getpid` liveness check. Never delete the lock file (especially on Windows, where deleting an open file fails).
- Do not support network filesystems (NFS/SMB home directories): `flock` semantics over NFS are advisory/emulated and unreliable. Detect and warn, or document as unsupported.

### 6. Testing

- **Unit tests** (`testing`, table-driven) for: config decode/encode incl. unknown-key preservation, junk-list matcher, project-id derivation, path normalization, exit-code mapping, three-way decision table (local-only / remote-only / both-same / both-different), and `git version` parsing. Inject `run(args, stdout, stderr)`; no globals, no `os.Exit` inside logic.
- **CLI integration** with `testscript` v1.16.0. `TestMain` calls `testscript.Main(m, map[string]func(){"doc-connector": func() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }})`; `TestScript` calls `testscript.Run(t, testscript.Params{Dir: "testdata/script", Setup: setup, UpdateScripts: *update})` (wire `-update` to that bool). Scripts are `txtar`, so fixtures (project repo, bare remote) are declared inline; `! exec doc-connector pull` asserts the non-zero exit code.
- **Hermetic git in tests:** set `HOME`/`USERPROFILE` to `$WORK/home`, `GIT_CONFIG_GLOBAL` to an empty file, `GIT_CONFIG_NOSYSTEM=1`, `GIT_AUTHOR_*`/`GIT_COMMITTER_*`, `LC_ALL=C`, and `git config --global core.autocrlf false` for Windows. Without this, a developer's global `commit.gpgsign`, hooks, or templates make tests machine-dependent.
- **Fake remote:** `git init --bare $WORK/remote.git` and use a `file://` URL (`file:///C:/...` on Windows) so the same string works on all platforms. Cover: empty remote (glob-fetch path), first push, fast-forward pull, delete propagation, divergence, binary file, nested directories, junk files, `.theirs.<host>` exclusion, and “docs are tracked” refusal.
- **CI matrix:** `ubuntu-latest`, `macos-latest`, `windows-latest` × Go `1.26.x` (floor) and `1.27.x` (current). Run `go vet ./...`, `go test ./...`, `go test -race ./...` on Linux/macOS (race detector on Windows amd64 is optional and slow), `golangci-lint run` on Linux, and `go mod tidy -diff` to catch module drift. Skip symlink-specific tests on Windows.
- Avoid `testify`: stdlib `t.Fatalf` with explicit field diffs is enough; one less module in the graph.

### 7. Release and distribution

# Optional, only after you own a bucket repository:

# scoops:

#   - repository:

#       owner: <owner>

#       name: scoop-bucket

#       token: "{{ .Env.SCOOP_BUCKET_GITHUB_TOKEN }}"

- `homebrew_casks` (available since GoReleaser v2.10) is the current recommendation; **`brews:` is deprecated** (“Homebrew Formulas (deprecated) — Deprecated in v2.10. Homebrew Casks should be used instead.”). A cask installs a plain binary via `binaries:`. Homebrew fetches with `curl`, so the download never receives the browser’s quarantine xattr — that is what keeps an unsigned binary usable.
- `scoops:` (plural key) requires a **separate bucket repository**; a manifest inside a subdirectory is invisible to `scoop bucket list`. Skip until there is a bucket repo.
- `winget:` generates manifests and can open a PR against `winget-pkgs`; it needs a fork/token and is not worth it for a personal tool in v1. Ship the `.zip` asset and document it.
- Not signing/notarizing macOS binaries is a real cost: users who download the zip via a browser see Gatekeeper’s “cannot be opened”. Homebrew cask installs are unaffected. If that becomes unacceptable, add a Developer ID + `notarytool` step later (requires a paid Apple account); do not block v1 on it.
- GitHub Actions release job: `actions/checkout@v7.0.1` with `fetch-depth: 0`, `actions/setup-go@v7.0.0` with `go-version-file: go.mod`, `goreleaser/goreleaser-action@v7.3.0` with `args: release --clean`, `permissions: contents: write`, and `HOMEBREW_TAP_GITHUB_TOKEN` as a PAT secret — the default `GITHUB_TOKEN` cannot push to the tap repository.
- `go install github.com/<owner>/doc-connector@latest` is the zero-infrastructure install path and works because the main package is at the module root; keep `go.mod`’s `go` directive at the true floor so this does not demand 1.27.

### 8. Logging, output, and exit codes

- **stdout** = the command’s result only. Human result text by default; exactly one JSON document with `--json`.
- **stderr** = diagnostics, progress, warnings, and errors. `slog` writes here, never to stdout.
- Color: enable only when `term.IsTerminal(stdout)` **and** `NO_COLOR` is unset/empty (per no-color.org) **and** `TERM != "dumb"`; on Windows additionally require `WT_SESSION`/`ConEmuANSI`/`ANSICON` (classic conhost may not have VT processing enabled). Offer `--color=auto|always|never` and `--no-color`; an explicit `always` overrides `NO_COLOR`, `never`/`--no-color` overrides everything, and `--json` implies `never`. Never emit ANSI into `--json`.
- Verbosity: default prints one summary line per command; `-v` adds per-file actions via `slog.Debug`; `--log-format=json` switches the **stderr** handler to `slog.NewJSONHandler` (useful in CI). `--quiet` suppresses the summary but not errors.
- One object per invocation (for `log`, include an array bounded by `--limit`, default 20). Do not emit NDJSON in v1; it complicates consumers and error handling.
- On failure, still print a JSON object: `{"schema_version":1,"command":"pull","ok":false,"error":{"code":"conflict","message":"…","hint":"…"}}`, then exit non-zero.
- Error codes are a closed set: `usage`, `precondition`, `conflict`, `git`, `io`, `internal`.

| Code | Meaning |
|---|---|
| 0 | Success, including “already up to date” and “nothing to push” |
| 1 | Unexpected/internal or I/O failure, lock timeout |
| 2 | Usage error: unknown command, bad flag, missing argument |
| 3 | Conflicts materialized; local files preserved, `.theirs.<host>` copies written |
| 4 | Precondition failed: not a git repo, docs not excluded, docs already tracked, `project_id` mismatch, shallow clone without `--project-id`, git too old, linked worktree |
| 5 | `git` subprocess failed (network, auth, non-fast-forward rejection); git’s own exit code and stderr are included in the message |
| 130 | Interrupted (SIGINT); cancel the context, release the lock, leave no temp files |

### 9. Path safety and ignore matching

- **Traversal:** `root, err := os.OpenRoot(docsDir)` once per command, then `fs.WalkDir(root.FS(), ".", fn)` and `root.ReadFile`/`root.Lstat`/`root.Rename`/`root.Remove`. `os.Root` rejects any path that escapes the root, follows symlinks only within the root, and rejects absolute symlink targets and Windows reserved device names. This is the correct stdlib answer and replaces hand-rolled `filepath.Clean` + prefix checks (TOCTOU-prone). On the first `pull`, `os.MkdirAll(docsDir, 0o700)` before `OpenRoot`.
- **Symlinks and special files:** `Lstat` each entry; if `ModeSymlink` or not regular, **refuse the whole operation** (exit 4) and list offenders. Following symlinks would let a “document” leak `~/.ssh` into the private repo, and Git would store the link target anyway.
- **Scope:** `os.Root` is an accident-prevention tool, not a security sandbox — hard links inside the tree can still reference inodes outside it, and `/proc`/device files are not excluded. Document that the docs directory is trusted local input.
- **Ignore matching:** no library. The rules are a fixed, closed set: exact basenames (case-insensitive): `.DS_Store`, `Thumbs.db`, `desktop.ini`, `.localized`, `Icon` followed by CR (`Icon\r`), `.Spotlight-V100`, `.Trashes`, `ehthumbs.db`, `$RECYCLE.BIN`; directories: `.git` (nested repository), `.svn`; reserved pattern: any basename containing `.theirs.` (conflict artifacts are local-only and never pushed). Implement as a `map[string]struct{}` lookup on `strings.ToLower(filepath.Base(rel))` plus one `strings.Contains`.
- Repo paths are always slash-separated: convert with `filepath.ToSlash` and compare with `path`, never `filepath.Match` (its separator is OS-dependent, so the same pattern behaves differently on Windows).
- If user-configurable patterns are ever added, use `github.com/bmatcuk/doublestar/v4` v4.10.3 (`**` support, slash-based) — but do not build a glob engine for a fixed junk list.

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|---|---|---|
| stdlib `flag` | `spf13/cobra` v1.10.2 | >10 commands, nested groups, generated completions/manpages required at launch. Costs pflag+mousetrap+yaml in the module graph and its own help format to stabilize. |
| stdlib `flag` | `urfave/cli/v3` v3.14.0 | You want a batteries-included `Command` struct with aliases and built-in completion, and accept its own parsing/help semantics. Requires `go 1.22`; still pulls testify/yaml in the test graph. |
| YAML v3 | `BurntSushi/toml` v1.6.0 | You want stricter syntax, no anchors, and flat config; users are comfortable with TOML. Loses comment-friendly nested sections and multi-line strings. |
| YAML v3 | `encoding/json` | Config is machine-written only, never hand-edited. Fails the “hidden human-editable config” intent. |
| System git | go-git v5.19.3 | A hard requirement to run without any git binary. Accept: no credential helpers, no `commit-tree`/`update-ref`/`rev-parse`/`check-ignore`, manual tree building via storer APIs, and v6 still beta. |
| `gofrs/flock` | `O_EXCL` PID file (exclusive create) | You want zero dependencies and will implement stale-lock detection (PID liveness + host boot check) yourself. Easy to get wrong across containers and Windows. |
| `gofrs/flock` | No lock | Read-only tool, or writes are already serialized by an external scheduler. Not true for a manual multi-terminal CLI. |
| Hand-rolled atomic write | `google/renameio/v2` v2.0.2 | Unix-only tool. It **exports no functions on Windows** by design, so it cannot be the cross-platform answer. |
| `testscript` | Plain `exec.Command` tests | Only a couple of scenarios, or you need in-process coverage instrumentation of `main`. `testscript` wins for multi-step CLI flows and golden output. |
| `goreleaser` | Hand-written `go build` matrix in CI | You need bespoke artifact logic GoReleaser cannot express. Otherwise you reimplement checksums, changelogs, and package manifests. |
| `x/term` | `Stat()` + `ModeCharDevice` | You want literally zero deps and accept false positives (e.g. `NUL` on Windows, some pseudo-terminals). |
| `os.Root` | `cyphar/filepath-securejoin` v0.6.1 | You must support Go < 1.24. Not applicable here. |

## What NOT to Use

| Avoid | Why | Use Instead |
|---|---|---|
| `github.com/go-git/go-git/v5` or `/v6` | `COMPATIBILITY.md` marks `commit-tree`, `read-tree`, `write-tree`, `update-index`, `update-ref`, `rev-parse`, `check-ignore` unsupported; credential helpers unsupported (issue #1420, maintainer-confirmed); v6 is `beta.1`. | System git via `os/exec`. |
| `gopkg.in/yaml.v3` | Frozen at v3.0.1 (2022); upstream repo marked unmaintained April 2025; the maintained module path explicitly retracts v3.0.0–v3.0.1. | `go.yaml.in/yaml/v3` v3.0.5. |
| `go.yaml.in/yaml/v4` | Still `v4.0.0-rc.6`; API churn. | v3.0.5. |
| `spf13/viper` v1.21.0 | Config precedence, env binding, remote providers, fsnotify — all unused; large transitive graph; hides which file was read. | Direct `yaml.Decoder` on one known path. |
| `google/renameio/v2` | README: “this package does not export any functions on Windows”; cites Go issue #22397. | Own temp+fsync+rename with Windows retry. |
| `natefinch/atomic` v1.0.1 | Last release 2021; wraps `MoveFileEx`, which Go’s `os.Rename` already does with `MOVEFILE_REPLACE_EXISTING`; adds no retry or durability. | Own helper (~40 lines). |
| `git2go` / libgit2 | cgo, shared library, kills the static single-binary requirement and cross-compilation. | System git or (if forced) go-git. |
| `fatih/color`, `mattn/go-colorable`, `charmbracelet/lipgloss` | Global color state / full styling engine for ~10 colored lines; `go-colorable` is unnecessary on Windows 10+ terminals with VT enabled. | Raw `\x1b[...m` constants behind one `colorizer` struct. |
| `sabhiram/go-gitignore`, `go-git/.../gitignore`, `doublestar` | Full gitignore/glob semantics for a fixed junk list; extra module and surprising edge cases (`**`, negation, anchoring). | Exact basename set + one reserved substring check. |
| `os.WriteFile` / truncate-in-place for docs | A crash or full disk leaves a truncated document; violates “never lose a change”. | Temp file + fsync + rename (+ retry on Windows). |
| `sh -c "git ..."` / string-concatenated commands | Shell injection via file names and remote URLs; quoting differs per platform. | `exec.CommandContext` with an argument slice. |
| Parsing `git status` / `git log --pretty` human output | Localized, quoted, and unstable across versions. | `-z` porcelain/plumbing (`ls-tree -z`, `diff-tree -z`, `status --porcelain=v2 -z` if ever needed). |
| Assuming a fixed 40-character object ID in *length checks* | SHA-256 repositories exist (`extensions.objectFormat=sha256`) and report 64 hex. | Treat the OID as an opaque hex string; validate `^[0-9a-f]{40,64}$`, then take the first 12 chars for the project id (frozen 2026-10-22). |
| `git add -A` / `git commit` in a store worktree | Requires a second worktree, runs hooks, and can commit unrelated state; the design deliberately has one copy of the documents. | Bare mirror + `update-index`/`write-tree`/`commit-tree`. |
| `--force` push | Would let a stale machine destroy remote history; conflicts must be surfaced. | Plain push (non-fast-forward rejected) + explicit pull/merge. |
| Setting `safe.directory` or `GIT_CONFIG_GLOBAL` automatically | Mutating global user configuration or disabling credential helpers is a consequential side effect and breaks auth. | Detect, explain, exit 4; let the user run the shown command. |

## Stack Patterns by Variant

- Use go-git v5.19.3 and reimplement the plumbing with `plumbing/object` + `storer` (build trees from `object.Tree` entries, write blobs with `Storer.SetEncodedObject`).
- Accept that credential-helper execution, git's exact config semantics (`includeIf`, `safe.directory`), and plumbing commands are gone; ship an auth model of your own (token env var or SSH key path). This is a different product, not a drop-in swap.
- Decode into `yaml.Node`, mutate the specific mapping keys, re-encode. Keep the same library; do not add TOML/JSON mirrors.
- Add a golden test that round-trips a comment-heavy fixture with an unknown key.
- Increase retries/backoff and, on final failure, keep the temp file, print its path, and exit 1 — never fall back to in-place writes.
- Keep the lock held for the whole operation so a second process cannot interleave.
- Cache the last local tree hash in config and skip `hash-object` when the file set's `(size, mtime, inode)` signature is unchanged; recompute hashes only for changed entries. Keep git as the source of truth for content.
- Read `extensions.objectFormat`; do not hardcode 40-hex length checks. Project id stays **`lower(<root commit OID>)[:12]`** (frozen in PROJECT.md), which works unchanged for 40-hex SHA-1 and 64-hex SHA-256 repos; all blob ids are opaque strings from git.
- Gate it behind an env var (`DOC_CONNECTOR_TEST_REMOTE`) and skip by default; local bare repos + `file://` URLs cover 95% of behavior without network flakiness.

## Version Compatibility

| Package A | Compatible With | Notes |
|---|---|---|
| `go 1.26.0` directive | Go 1.26.x, 1.27.x | Build with 1.27.3; test the 1.26 floor in CI. 1.25 is EOL (two newer majors shipped) and cannot build `x/term v0.47.0`. |
| `golang.org/x/term v0.47.0` | `go 1.26.0`, `x/sys v0.49.0` | Its `go.mod` is the binding constraint on the module floor. |
| `github.com/gofrs/flock v0.13.1` | `go 1.25.0`, `x/sys v0.47.0` | MVS raises `x/sys` to v0.49.0; both compile with go 1.26. |
| `go.yaml.in/yaml/v3 v3.0.5` | `go 1.16` | No constraint; v3.0.0/v3.0.1 are retracted under this path. |
| `go-internal v1.16.0` | `go 1.25` | Test-only; never linked. |
| Go 1.27-built binaries | macOS 13+, Windows 10+, Linux 3.2+ | Go 1.27 dropped macOS 12; users on older macOS must build with Go 1.26. |
| system git 2.30 | All commands used | `rev-list --max-parents=0`, `update-index --index-info -z`, `commit-tree`, `ls-tree -z`, `cat-file --batch`, `check-ignore`, `rev-parse --is-shallow-repository`, `init --initial-branch` (2.28+) are all available. Do **not** use `rev-parse --path-format` (2.31+) or `cat-file --batch-command` (2.36+). |
| system git 3.0 (future) | `--git-dir` required | `safe.bareRepository` will default to `explicit`; every store invocation already passes absolute `--git-dir`, so we are forward-compatible. |

## Sources

- <https://go.dev/dl/> — current stable `go1.27.3` (2026-10-19).
- <https://go.dev/doc/devel/release> — release dates, patch levels, and the “supported until two newer major releases” policy.
- <https://go.dev/doc/go1.27> — `encoding/json` now backed by v2 (v1 API preserved), macOS 13 minimum, new stdlib packages.
- <https://pkg.go.dev/os#Root> and `go doc os.Root` on Go 1.27.2 — `OpenRoot`, `Root.FS`, `Root.Rename`, `Root.WriteFile`, `Root.MkdirAll`, `Root.RemoveAll`, symlink/escape semantics.
- <https://pkg.go.dev/os#Rename> — “Even within the same directory, on non-Unix platforms Rename is not an atomic operation.”
- <https://github.com/golang/sys/blob/master/windows/syscall_windows.go> — `MoveFileEx(..., MOVEFILE_REPLACE_EXISTING)` behind `os.Rename` on Windows.
- <https://github.com/yaml/go-yaml> — YAML-org takeover, maintenance status, v3 compatibility notes.
- <https://pkg.go.dev/go.yaml.in/yaml/v3> — v3.0.5; retraction of v3.0.0/v3.0.1 under the new module path; `,inline` semantics for structs and maps. Verified locally: an unknown key (`b: 2`) plus a nested unknown mapping survived decode→encode through an inline `map[string]any`.
- <https://proxy.golang.org/gopkg.in/yaml.v3/@latest> — frozen at v3.0.1 (2022-05-27).
- <https://github.com/go-git/go-git/blob/master/COMPATIBILITY.md> — unsupported plumbing (`commit-tree`, `read-tree`, `write-tree`, `update-index`, `update-ref`, `rev-parse`, `check-ignore`).
- <https://github.com/go-git/go-git/issues/1420> — credential helpers unsupported, maintainer statement 2025-02-03.
- <https://github.com/go-git/go-git/releases> — `v5.19.3` stable and `v6.0.0-beta.1` (2026-10-04).
- <https://pkg.go.dev/github.com/gofrs/flock> and `flock_unix.go` / `flock_windows.go` — `unix.Flock` vs `LockFileEx`, `TryLockContext`.
- <https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript> and `testscript/doc.go` — `testscript.Main`, `testscript.Run`, `Params.UpdateScripts`.
- <https://git-scm.com/docs/git> — `GIT_TERMINAL_PROMPT` (“if set to false, git will not prompt on the terminal”).
- <https://git-scm.com/docs/git-config#Documentation/git-config.txt-safedirectory> — `safe.directory` and `safe.bareRepository` (Git 3.0 default `explicit`).
- <https://git-scm.com/docs/git-config#Documentation/git-config.txt-includeIf> — `gitdir`, `onbranch`, `worktree` conditional includes.
- <https://git-scm.com/docs/git-check-ignore> — exit status 0/1/128.
- <https://git-scm.com/docs/git-hash-object>, <https://git-scm.com/docs/git-update-index>, <https://git-scm.com/docs/git-commit-tree>, <https://git-scm.com/docs/git-ls-tree>, <https://git-scm.com/docs/git-cat-file>, <https://git-scm.com/docs/git-fetch>, <https://git-scm.com/docs/git-push> — plumbing contracts; behaviors verified locally against git 2.47.3 (see below).
- Local verification (git 2.47.3, Go 1.27.2): `read-tree --empty` + `update-index -z --index-info` (add and mode-0 delete) + `write-tree` + `commit-tree` + `update-ref` against a bare repo; `hash-object -w --no-filters --stdin-paths` order/paths; empty-remote `fetch` with explicit refspec fails, glob refspec succeeds; `rev-list --max-parents=0`; `rev-parse --is-shallow-repository`; `--absolute-git-dir` vs relative `--git-common-dir` in linked worktrees; `commit-tree` without identity fails with “Author identity unknown”.
- <https://goreleaser.com/customization/publish/homebrew_casks/> — casks since v2.10.
- <https://goreleaser.com/customization/publish/homebrew_formulas/> — “Deprecated in v2.10. Homebrew Casks should be used instead.”
- <https://goreleaser.com/customization/publish/scoop/> — `scoops:` key and bucket repository requirement.
- <https://goreleaser.com/customization/publish/winget/> — manifest generation and PR flow.
- <https://github.com/goreleaser/goreleaser/releases> — v2.18.3.
- <https://github.com/goreleaser/goreleaser-action/releases> — v7.3.0.
- <https://github.com/actions/setup-go/releases> — v7.0.0; <https://github.com/actions/checkout/releases> — v7.0.1.
- <https://github.com/golangci/golangci-lint/releases> — v2.15.0.
- <https://no-color.org> — `NO_COLOR` convention (present and non-empty disables color).
- <https://github.com/google/renameio> — README: no exported functions on Windows, citing Go issue #22397.
- <https://proxy.golang.org/> — exact latest versions for `cobra v1.10.2`, `urfave/cli/v3 v3.14.0`, `BurntSushi/toml v1.6.0`, `viper v1.21.0`, `doublestar/v4 v4.10.3`, `x/sys v0.49.0`.

<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->

## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->

## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->

## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->

## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:
- `/gsd-fast` for a trivial task inline, with no subagents and no PLAN.md
- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->

<!-- GSD:profile-start -->

## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
