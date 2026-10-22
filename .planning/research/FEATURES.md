# Feature Research

**Domain:** project-scoped private document synchronization over git
**Researched:** 2026-10-22
**Confidence:** HIGH for prior-art mechanisms (verified against primary docs); MEDIUM for effort grades (planning estimates, not commitments)

## Scope and Method

This survey answers one question: **what must `doc-connector` ship, what can it uniquely offer, and what must it refuse to build?**

The real competitor is not a file-sync product. It is the hand-rolled bare-repo script a developer writes when they want private notes versioned but cannot put them in the team repository. Every surveyed tool solves an adjacent problem: large-file transport (git-annex, git-lfs), continuous folder replication (Syncthing, Unison, rclone), dotfile management (chezmoi, yadm), or encryption (git-crypt, sops). None combines:

1. deterministic per-project identity derived from the **main** repository,
2. plain-file git history in a **separate** private repository,
3. a hard guarantee that the main repository's refs, objects, index, and tracked files are untouched,
4. local-wins conflict copies instead of silent overwrite or auto-merge.

That gap is the product, and it follows the Key Decisions in `PROJECT.md` [S31]. The feature set below is organized around it.

**Effort legend** (one experienced Go developer, including unit and integration tests, excluding multi-week CI yak-shaving):

- **S** — up to ~2 dev-days
- **M** — ~3–7 dev-days
- **L** — more than ~1 week

**Git floor check:** every plumbing command used by this design exists in git 2.30. In particular, `git rev-parse --path-format` is 2.31+ and must not be used; path joining stays in Go. Empty remotes are probed with `ls-remote` before fetching, because fetching a branch that does not exist yet fails. [S32][S33]

**Identity formula (frozen 2026-10-22):** `project_id = lower(root_hex)[:12]`, where `root_hex` is the lowercase 40-hex (SHA-1) or 64-hex (SHA-256) OID of the single root commit from `git rev-list --max-parents=0 HEAD`; no second hash, salt, or case change. Empty/unborn HEAD, multiple roots, or a shallow clone without `--project-id` refuses with exit 2; a multi-root history never takes the smallest. Test vector: root `743c420a8e7b11c3382c49dfec71987cb7a6f710` → id `743c420a8e7b`. The brief's `sha256(ASCII(root_hex))[:12]` would yield `8d818c0fc915` for the same root and must not be mixed; switching later requires `--project-id` or manual migration. SHA-256 repositories have a 64-hex root and derive a different ID; `config.project.object_format` records it for diagnostics only. Config validation accepts only 12 lowercase hex characters [S31][S34].

## Feature Landscape

### Table Stakes (Users Expect These)

Core commands, state handling, and the safety contracts that make a sync tool trustworthy. Missing any row here means the tool is either unusable or unsafe.

| Feature | Why Expected | Complexity | User-Visible Behavior | Depends On |
|---------|--------------|------------|-----------------------|------------|
| `init` bootstrap and adoption | Nothing works until a clone is bound to a private remote and a project prefix; new machines must recover cleanly because config is intentionally not cloned | M | `init --remote <url>` validates the repo, derives the project ID, creates the docs dir if missing, writes `.git/doc-connector/config.yaml`, adds the exclude block, detects the remote default branch (or `--branch`), probes with `ls-remote`, bootstraps the mirror for an empty remote (no fetch of a nonexistent branch) or fetches the bare mirror, and reports local/remote file counts plus the next command. Idempotent on re-run with the same remote/docs dir; changing either requires `--force`. If docs and the remote prefix both exist with different content, refuses; `--adopt-local` treats local docs as authoritative, while remote-wins is a documented move-aside + pull, never automatic | Root-commit identity, pollution guard, exclude writer, store bootstrap |
| Stealth exclude writer | The core promise is that `git status` and `git add -A` never see private docs, without touching the team's shared `.gitignore` | S | Appends a marked, idempotent block (`# doc-connector:begin` … `/private-docs/`) to `.git/info/exclude`; never edits `.gitignore`; verifies the path is ignored with `git check-ignore` | Nothing (foundation) |
| Pre-write pollution guard | One accidental tracked file or index write destroys the product's reason to exist; refusing is better than discovering pollution later | M | Before every mutating command: verify inside a work tree, docs path is untracked, docs path is a real directory (not a symlink), store lives under `.git/doc-connector/`, and — when config exists — `project_id` matches the derived ID or the recorded explicit override. `init` writes the exclude block, then re-checks that `git check-ignore` reports the path ignored [S25]. Any failure exits 3 with the exact failing check | Exclude writer, identity, config |
| `status` with divergence summary and per-file diff | Users must know "is this current?" without reading git plumbing output | M | Prints `clean`, `ahead`, `behind`, or `diverged`, counts per direction, and per-file `A`/`M`/`D`/`C` lines (C = conflict). Compares content, not timestamps. Fetches the remote tip by default into the mirror (main repo untouched); `--offline` uses the last fetched tip and shows its age; a failed default fetch prints cached state and exits 4 | Mirror fetch, base state, content hashing, three-way classifier |
| `push` | The primary write path; must publish only this project's prefix and never rewrite unrelated projects | M | Captures docs into blobs in the mirror, builds a subtree for `<project_id>/`, splices it onto the fetched remote tip (root commit if the remote is empty), commits with machine metadata, pushes, updates base, and prints the new commit and change counts. No-op when nothing changed. Refuses if this project's prefix advanced past base (pull first) or if base history was rewritten (explicit adoption); a push race is retried only when the competing change touched a different project prefix | Status classifier, tree builder, remote fetch, ignore rules, byte fidelity |
| `pull` | The primary read path; must apply adds/modifies/deletes without ever destroying local work | L | Fetches, classifies every path against base/local/remote, applies clean remote changes atomically, keeps local files on conflicts, writes conflict copies, and reports applied/conflicted counts. Base advances only when no conflicts remain and every planned write succeeded | Three-way classifier, deletion rules, atomic writer, conflict artifacts |
| `log` | "What synced, when, from which machine?" is a basic audit need and the source for `restore` | S | Lists commits touching this project's prefix with commit ID, time, machine trailer, and changed paths. Filters by `--path`; defaults to the remote tip, not orphaned local commits | Mirror history, project prefix |
| `restore` | Users delete files and need old versions back; git history is the whole point of choosing git | M | `restore <path> [--from <rev>] [--to <dir>]` extracts the blob from any mirror commit (default: the current remote tip). Refuses to overwrite an existing differing file without `--force`; can restore a file deleted in the current tree | `log`/history, blob reader, path safety |
| `--dry-run` on every mutating command | Sync tools are destructive by nature; users must see the plan before the first real run | S | Prints the exact files/commits/actions that would occur and writes nothing. Exits with the code the real run would return (so a would-be conflict exits 1); modeled on `rclone -n` and `git push -n` | Shared plan layer used by push/pull/restore/init |
| Stable exit-code contract | Scripts, aliases, and CI cannot branch on prose; git's coarse success/failure convention is too coarse for "conflict vs auth failure" | S | `0` success/no-op; `1` user action required (divergence, conflict, overwrite refusal, ambiguous adoption); `2` usage/config; `3` safety/precondition; `4` remote/network/auth; `5` local I/O/integrity. Documented in `--help` and stable across releases | Nothing (contract) |
| Machine identity | Conflict artifacts and commits must say *which* machine produced them; hostnames alone collide and change | S | `init --machine laptop` or config `machine: laptop`; defaults to a sanitized hostname. Written as a commit trailer and used in conflict suffixes; commit author defaults to `doc-connector <doc-connector@<machine>>` unless configured. Override is explicit; no device registration or pairing | Config |
| Three-way divergence classification | The only way to distinguish "local changed", "remote changed", and "both changed" without guessing; the last-synced commit is the base | M | Every path resolves to one of: unchanged, local-only change, remote-only change, both-same, both-different, local-add, remote-add, local-delete, remote-delete, modify/delete. Shared by `status`, `push`, and `pull` | Base state, content hashing |
| Deletion propagation | Users expect deletes to propagate; they also expect a remote delete not to silently erase a locally edited file | M | Clean delete (one side deleted, other unchanged) is applied. Modify/delete and delete/modify are conflicts: the local state is kept, nothing is erased, and the path is listed as conflicted until explicitly resolved | Three-way classifier |
| Conflict reporting | Silent overwrite is the one failure the user explicitly cannot accept; every conflict must be visible and must fail the command | S | Lists each conflicted path with reason (`both modified`, `remote deleted`, `local deleted`) and exits 1; modify/modify writes `<name>.theirs.<machine>` with the remote bytes. `status` shows the same set; `push` refuses while conflicts are unresolved; resolve with `push --resolve <path>` (local wins) or `restore --force <path>` (remote wins) | Three-way classifier |
| Atomic apply and single-instance lock | A half-written document or two concurrent runs is data loss, not a cosmetic bug | M | Pull writes each file to a temp file in the same directory, fsyncs, and renames into place; directories are removed only when empty. `.git/doc-connector/lock` uses O_EXCL with PID/host and stale-lock detection; a second concurrent mutating command exits 3 | `pull`, filesystem primitives |
| Built-in junk ignore list | `.DS_Store`, `Thumbs.db`, and friends otherwise pollute every commit and create noisy conflicts | S | Built-ins: `.DS_Store`, `._*`, `Thumbs.db`, `ehthumbs.db`, `desktop.ini`, `.git/`, and `*.theirs.*`. Applied to status, push, and pull; conflict copies can never be re-synced, and `*.theirs.*` is a documented reserved pattern | Tree builder, content hashing |
| Any git remote, system credentials | The tool must work with GitHub, Bitbucket, self-hosted, SSH, HTTPS, and `file://` without provider SDKs or its own auth | S | Shells out to system `git`; reuses SSH agent, credential helpers, proxy, and `insteadOf`. Warns when credentials are embedded in the remote URL because they would land in hidden config | `init`, fetch/push |
| Offline-capable inspection | Networks fail; users still need to know what is pending and what history exists | S | `status --offline` skips fetch and prints the age of the last fetched remote tip; `status` otherwise fetches by default and falls back to the cached tip with exit 4 on failure. `log` always reads the local mirror | Mirror, base state |
| Path safety and portability | Pull/restore write attacker- or accident-controlled paths; Windows/macOS differ on case and filename rules | M | Rejects absolute paths, `..`, paths escaping the docs dir, symlinked parents, and reserved Windows names for paths derived from repo content; an explicit `restore --to <dir>` is validated separately. Refuses to sync symlinks in v1 rather than silently following or dropping them. Case-collision detection refuses ambiguous paths on the current platform; NFD/NFC normalization is v1.x | `pull`, `restore`, tree builder |
| Cross-platform behavior (Linux, macOS, Windows) | One static binary and identical commands are a stated constraint; filesystem semantics differ in ways that can corrupt writes | M | Commands avoid POSIX-only path assumptions; path separators, case-insensitive filesystems, Windows replace/open semantics, directory fsync behavior, and CRLF are covered by tests. Symlinks are refused on all platforms in v1; platform-specific support is v1.x | Path safety, atomic writer, byte fidelity |
| Byte-exact file fidelity | Notes include PDFs, images, and office files; a CRLF filter or text conversion corrupts them and breaks hashes | S | Uses `git hash-object --no-filters` (or native Go blob hashing) for raw bytes [S22] and NUL-delimited `ls-files`/`status` output where supported; preserves executable bit; refuses symlinks in v1. Git does not preserve mtimes or empty directories — documented, not "fixed" | Tree builder, hashing |
| Size guard without LFS | Git stores a full blob per version; a few 200 MB files silently balloon the private repo and clone times | S | `push` refuses above 100 MB by default (explicit `--allow-large` override); `status` and `pull` warn above 50 MB. Never silently skips a file | `push`, `status` |
| Multi-machine setup flow | The hidden-config decision means every clone is a fresh `init`; users need a predictable second-machine path | S | Documented flow: clone team repo → install binary → `init --remote ... --machine desktop` → `pull`. If the project prefix already exists and local docs are empty, `init` adopts the remote and tells the user to pull. No secrets are stored | `init`, identity, adoption |
| CLI ergonomics: help, version, non-interactive | A CLI that prompts unpredictably cannot be scripted or used over SSH | S | `--help` for every command, `--version`, and no interactive prompts: destructive adoption requires the explicit `--adopt-local` flag. Errors go to stderr; machine-readable status is v1.x | Nothing |

#### Observable command shape (v1 contract sketch)

```text
$ doc-connector status
project  743c420a8e7b  private-docs/
remote   git@github.com:me/private-docs.git (main)
base     9c1e4a2  remote 4a7b91c  fetched 2m ago
state    diverged  ahead 3  behind 2  conflicts 1
 M  notes/architecture.md
 A  notes/new-idea.md
 D  notes/old.md
 C  notes/plan.md  -> notes/plan.md.theirs.laptop

$ doc-connector pull
applied 2 changes; 1 conflict
kept local: notes/plan.md
wrote:      notes/plan.md.theirs.laptop
exit 1
```

### Differentiators (Competitive Advantage)

These are not required for a generic sync tool, but they are why this tool exists instead of a shell script. Each maps to a Key Decision in `PROJECT.md`.

| Feature | Value Proposition | Complexity | User-Visible Behavior | Depends On |
|---------|-------------------|------------|-----------------------|------------|
| Deterministic project identity from the root commit | The same repository cloned anywhere yields the same 12-hex ID, with no local UUID, database, or registration; the formula (direct 12-hex truncation of the root OID) is frozen before the first push because IDs are write-once. Renaming or moving the project does not change it | M | `init` prints `project 743c420a8e7b (from root 743c420a…)`. Shallow clones and empty repos refuse with exit 2 and require `--project-id`; multi-root histories refuse unless `--project-id` is given. An explicit override that disagrees with a derivable root ID is refused unless `--force`. Explicit IDs are marked as such in config and survive shallow clones | `git rev-list --max-parents=0` [S23], shallow detection [S27], non-replace traversal [S29], frozen digest [S34], config |
| Zero-pollution guarantee for the main repository | The team repository's history, refs, index entries, object database, hooks, and tracked files are untouched by doc-connector. This is stronger than "we only write an ignore file" | L | Every git operation for docs targets `.git/doc-connector/store.git` with its own temp index (`GIT_INDEX_FILE`, `update-index --cacheinfo`) [S26]; the main repo is only read (`rev-parse`, `ls-files`, `check-ignore`, `rev-list`) [S25][S27]. Integration test asserts `HEAD`, `status --porcelain=v2`, `for-each-ref`, and tracked-file set are identical before/after every command. The only allowed writes are `.git/info/exclude` and `.git/doc-connector/` | Exclude writer, guard, separate `--git-dir`, temp index |
| Shared multi-project private store | One private repository for all projects, one ACL to manage, no repo sprawl. Each project owns exactly one top-level `<project_id>/` subtree | M | `init --remote` on project B reuses the same URL; `push` replaces only B's top-level tree entry, preserves A's subtree and any root README, and retries once or twice when a non-fast-forward race came from a different prefix; a same-prefix race is treated as divergence and refused. `status` shows only B's paths. Each project keeps its own local mirror (self-contained and deletable); only the remote is shared | Tree splicing (`ls-tree`/`mktree`/`commit-tree`) [S24][S30], fetch/retry |
| Honest content-hash status | Reports what actually changed, not what a timestamp suggests. A `touch`, a file copy, or a checkout with new mtimes does not create fake diffs; a same-size edit is not missed | M | Compares git blob OIDs for local, base, and remote. `status --json` (v1.x) exposes `local_oid`/`base_oid`/`remote_oid` per path. Hashing every file is acceptable at hundreds of files; no mtime cache in v1 | Content hashing, base state |
| Local-wins per-file conflict copies with machine identity | The safest possible default for prose and binary documents: local content is never destroyed, the remote version is preserved beside it, and the conflict is visible in filenames and exit codes | M | On modify/modify: keep local, write `<name>.theirs.<machine>` (`.2`, `.3` if taken) with the exact remote bytes, ignore the artifact from all future syncs, exit 1. On modify/delete: keep the local decision (file or deletion), list the conflict, exit 1. Resolution is explicit: `push --resolve <path>` re-asserts local, or `restore --force <path>` takes remote; `push` refuses otherwise | Machine identity, three-way classifier, artifact ignore, resolution flag |
| Offline-first audit trail | History and pending state remain inspectable on a plane or when the remote is down; commits carry enough metadata to audit without a server | S | `log` reads the mirror without network; commits include `Doc-Connector-Project`, `Doc-Connector-Machine`, and tool version trailers; `log --path` filters within the project | Mirror, commit metadata |
| Identity survives project moves and renames | The hash is derived from history, not from a filesystem path or remote URL, so reorganizing a workspace does not orphan documents | S | Moving the project directory or renaming the team remote leaves `project_id` unchanged; `init` re-derives and verifies the same ID | Root-commit identity |

### Anti-Features (Commonly Requested, Often Problematic)

Documented so they do not creep into v1. Each is a plausible request with a concrete cost; "Complexity If Built" is the cost avoided by refusing it.

| Feature | Why Requested | Complexity If Built | Why Problematic | Alternative |
|---------|---------------|---------------------|-----------------|-------------|
| Built-in watch mode / daemon / scheduler | "It should just stay in sync"; Syncthing and Unison both offer continuous modes | L | A daemon sees partial editor writes, temp files, and rename storms; it creates conflict commits while the user is away; file watchers are unreliable on WSL, network shares, and some editors. It also turns a deterministic CLI into a process to supervise, lock, and debug. The user explicitly chose manual CLI | Manual `pull` before work and `push` after; an optional user-owned alias/cron line that calls the idempotent `push` (not shipped as a supervisor) |
| End-to-end encryption | Private notes feel like they should be encrypted at rest in the remote | L | Key distribution, rotation, and loss become the new failure mode; losing the key loses the entire history. Deterministic encryption leaks equality; encrypted blobs cannot be diffed, merged, or restored without an unlock step. git-crypt itself documents no revocation, no filename encryption, and no delta compression | Private-repository ACL is the boundary; OS full-disk encryption for local disks; password manager for actual secrets. Secrets are explicitly out of scope |
| Secrets management | Users will inevitably keep a token or key in a note | L | sops/KMS/age add recipients, key services, rotation, and CI assumptions; a sync tool that also claims secret management invites storing exactly the material the threat model excludes. Compromise of the private repo compromises the secrets | Password manager (1Password/Bitwarden/etc.); `.docignore` (v1.x) for local-only files; never store credentials in the docs tree |
| Auto-create the private remote | "One command should set everything up" | L | Requires provider API tokens, OAuth scopes, org policy handling, 2FA, and per-provider implementations that break independently. It also makes the tool responsible for choosing visibility — a security decision | Document one command (`gh repo create <name> --private`) or the web UI; `init --remote` accepts any existing URL, including `file://` and self-hosted |
| GUI / mobile app / web UI | Non-terminal users, file pickers, visual diffs, access from a phone | L | Cross-platform GUI toolkits and web frontends break the single-static-binary story; accessibility, theming, auth, hosting, and packaging multiply the surface; the core value is deterministic scripting, not browsing | CLI plus the user's editor/diff tool; `log`/`status` output is designed to be readable and (v1.x) JSON-parseable |
| Git LFS | "What if a document is large?" | L | LFS replaces content with pointers, requires an LFS endpoint and client on every machine, adds `.gitattributes` configuration, and makes `git show` insufficient for restore. Conflicts on pointer files are useless. Quotas and server support vary. Target docs are notes and business files, not GB-scale media | Plain blobs with a 50 MB warning / 100 MB refusal; keep large media out of the docs tree |
| Cloud storage APIs (S3, Drive, Dropbox, WebDAV) | "Sync without git hosting" | L | Each backend needs SDKs, credentials, pagination, retries, and eventual-consistency handling; none provides git history, three-way base comparison, or `restore`. It turns a git tool into a sync platform | Any git remote: GitHub, Bitbucket, self-hosted, SSH, or `file://` on a mounted disk |
| Automatic text merge | "Just merge non-conflicting lines" | L | Prose merges produce plausible but semantically wrong documents; office/PDF files are not line-mergeable; silent auto-merge is exactly the "conflict caused content loss" failure the user named. Line-based merge also destroys the clean "local vs theirs" audit | Keep local, write `.theirs.<machine>`, let the human merge with their own diff tool. `push --resolve` records the decision |
| Hidden refs in the team repository (`refs/private/*`) | No second repository to create; docs travel with the clone; one remote | M | Pushing uploads objects and refs into the team repo, requires write access the user often lacks, leaves private data in the object database and server backups even after ref deletion, and exposes ref names to admins. It directly violates the zero-pollution guarantee. Git's own push semantics send "all necessary data" to that repository | A separate private repository. This is the foundational Key Decision [S31] |
| Force push / `--force` on the shared store | "Fix divergence quickly" | S | Rewrites shared history, can orphan another machine's synced commits, and makes restore/audit ambiguous. It is the opposite of "never lose changes" | Append-only commits; fetch, re-splice the project prefix, retry on race; conflicts produce copies. If history was rewritten externally, refuse and require explicit adoption |
| Auto-install git hooks | "Run before every commit automatically" | S | Writes `.git/hooks` in the team repo, surprises collaborators on shared machines, can block unrelated team commits, and is invisible in code review. Hooks are also not cloned, so behavior differs per machine | A wrapper binary and shell alias; document the manual `push`. No hook installation |
| One-command `sync` (pull + push) | Fewer keystrokes | S | Hides direction and divergence; encourages blind conflict resolution; makes exit-code semantics ambiguous ("did it pull, push, or both?"); a failed push after a successful pull needs different remediation | Explicit `pull`, inspect `status`, then `push`; a user alias is fine because the primitives stay honest |
| One docs directory shared by multiple projects | "One notes folder for everything" | M | Ownership, identity, and deletion become ambiguous: one project's push can rewrite another's files, and there is no single root commit to derive a prefix from. Conflict copies would collide across projects. `PROJECT.md` explicitly excludes it | One `<project_id>/` prefix per project in the shared store; share reference material by copy or symlink outside the sync tool |
| Tracking docs in the main repo with `skip-worktree` / `assume-unchanged` | "Hide tracked files from status" | S | These are local index flags, fragile across resets/checkouts, and the files remain tracked — one commit and they are in team history forever. They also change index state, violating non-pollution | `.git/info/exclude` for genuinely untracked files, verified by `git check-ignore` |

## Prior Art Survey

Versions checked 2026-10-22 where the project publishes one: git-annex 10.20261020, Syncthing docs v2.1.0, chezmoi 2.73.3, git-crypt 0.8.0, Git LFS 3.8.0, git docs 2.56.0. **Fit:** 0 = unrelated problem, 5 = near drop-in.

| Tool | Mechanism | Fit | Why it is not a direct substitute |
|------|-----------|-----|-----------------------------------|
| **git-annex** | Annexed files are symlinks/pointers to a key-value store under `.git/annex`; git tracks names and metadata; location tracking records which repos hold which content; `copy`/`get`/`sync --content` move content according to preferred-content rules [S1][S2] | 3/5 | Requires git-annex on every machine, not just git. A plain clone yields links/pointers without content, so restore depends on the annex store and its location data. It is designed for large/archival files and partial checkouts, not for a project-hash-scoped tree of plain notes. Conflict handling is built around annex keys, not a per-file three-way merge of plain blobs [S2][S3] |
| **Syncthing** | Continuous peer-to-peer block sync; each folder has an index database; changes detected by watcher plus periodic full scans; conflicts rename one side to `<filename>.sync-conflict-<date>-<time>-<modifiedBy>.<ext>` [S4] | 2/5 | A long-running daemon with device pairing, not a manual CLI. No git history or commit-level restore; optional file versioning is per-device, not a shared base. Conflict copies are excellent prior art (and validate our `.theirs` design) but are the entire conflict model; there is no user-visible base commit and no per-project identity tied to a team repository. `.stignore` is a separate pattern language [S5] |
| **Unison** | Bidirectional sync between two replicas over SSH/TCP; per-root-pair archive files under `~/.unison`; conflicts detected and displayed; `-copyonconflict` keeps copies of files that would otherwise be overwritten or deleted; `-repeat watch` enables continuous mode [S6][S7][S8] | 2/5 | Two replicas and local archives, not a shared remote with history. Both ends need compatible Unison binaries and matching archives; there is no third-party audit trail or `git log`. Conflict copies are optional and not machine-suffixed in a durable, restorable way. No project identity, no multi-project store |
| **rclone** | `sync` makes destination identical to source and deletes destination files; `bisync` adds bidirectional state; comparison by size+mtime or checksum; `--dry-run`; numbered exit codes [S9][S10] | 1/5 | One-way mirror by design (bidirectional mode is a separate stateful protocol); no git history, no per-file three-way base, no `restore`. Cloud/credential configuration is a large surface. Its `--dry-run` and exit-code discipline are worth copying, not its storage model |
| **chezmoi** | Single Go binary managing dotfiles: a source directory is rendered (templates, password-manager lookups) and applied to `$HOME`; supports encryption; one-command bootstrap on a new machine [S11][S12] | 1/5 | The target model is per-user dotfiles in `$HOME`, not project-scoped documents inside an untrusted team repository. Templates and `apply` solve machine differences, not three-way document sync. No shared per-project store, no team-repo exclusion guarantee |
| **yadm** | A git wrapper that operates on dotfiles (work tree is the home directory), with alternate files by OS/host and optional encryption/transcrypt/git-crypt integration [S13][S14] | 1/5 | Same dotfile scope as chezmoi, implemented as a git wrapper. It inherits git's line-based merge conflicts, which is wrong for prose/binaries, and has no concept of a project prefix, a shared private store, or a polluted-team-repo guard |
| **git-crypt** | Clean/smudge filters encrypt selected files in place; `.gitattributes` marks paths; GPG or symmetric key; transparent on checkout/commit [S15] | 0/5 | Orthogonal to sync: it encrypts content in whatever repository already holds it. It does not encrypt filenames or metadata, cannot revoke access or rotate keys, breaks delta compression, and produces undiffable/conflicted ciphertext. It is a component, not a transport |
| **sops** | Encrypts values inside YAML/JSON/ENV/INI/binary files using KMS/age/PGP; leaves structure and keys visible; an editor, not a sync engine [S16] | 0/5 | Solves secret-at-rest in structured config, not document transport or history. Requires key-service recipients and rotation. Using it here would drag secrets management into scope, which `PROJECT.md` explicitly excludes |
| **git-lfs** | Replaces large files with text pointers in git; content lives on an LFS server; requires `git lfs install` and tracked `.gitattributes`; restore needs the LFS client and server [S17] | 1/5 | Adds a server-side LFS endpoint, a client dependency, and pointer indirection. `git show`/`cat-file` no longer return document bytes. Conflicts on pointers are not meaningful. For <100 MB notes it is pure overhead; a size guard is the honest v1 answer |
| **`git worktree`** | Adds linked working trees to the *same* repository; they share the object database and refs, differing only in per-worktree files such as `HEAD` and index [S18] | 0/5 | A private docs branch checked out in a linked worktree still creates objects and refs in the team repository and records metadata under `.git/worktrees`. It solves parallel checkouts, not privacy or exclusion. It cannot satisfy the zero-pollution guarantee |
| **`git sparse-checkout`** | Reduces the working tree to a subset of **tracked** files (cone mode by default); writes sparse-checkout config; experimental behavior [S19] | 0/5 | It only hides tracked paths from the working tree. Untracked private docs are unaffected and still show in `git status` unless separately ignored; tracked docs remain in team history. Enabling it changes repository config and affects the whole worktree. It is a checkout optimization, not an ignore or sync mechanism |
| **Ad-hoc `git push origin HEAD:refs/private/*`** | Pushes a private commit to a hidden ref in the team repository; git sends all objects the remote lacks [S21] | 0/5 | Requires write access to the team remote (often absent), stores private objects and a ref visible to anyone with repo access in the team repo and its backups, and cannot be cleaned reliably by deleting the ref. No per-file conflict UX, no project scoping, no restore semantics. Directly violates the foundational non-pollution decision |
| **Hand-rolled bare-repo script** | `git --git-dir=store.git --work-tree=private-docs add/commit/push`, plus a hand-maintained exclude line | 4/5 | The true baseline and the closest fit. It typically lacks: a pollution guard, deterministic project identity, adoption of an existing remote prefix, three-way deletion rules, machine-suffixed conflict copies, a lock, atomic writes, and shared-store prefix splicing. Those missing pieces are the product |

### What the survey changes in our design

- **Conflict copies are validated, not novel.** Syncthing and Unison both preserve the losing side; we keep the local side, name the remote copy with a stable machine identity, and make it git-restorable. That combination is the differentiator, not the existence of copies [S4][S7].
- **Hash, do not trust mtimes.** Unison's default `fastcheck` uses mtimes for a first pass, rclone compares size+mtime or checksum, and Syncthing uses mtime/size/permissions before rehashing [S4][S7][S9]. A doc tool that says "modified" after a `touch` trains users to ignore status. Content OIDs are the honest primitive and are cheap at this scale.
- **Ignore syntax is settled.** Git's pattern language is the lingua franca; Syncthing's `.stignore` shows the value of a root-level ignore file [S5][S20]. v1 ships a built-in junk list and always ignores `*.theirs.*`; a synced `.docignore` using gitignore syntax is a v1.x addition.
- **Dry-run and exit codes are table stakes.** rclone exposes both as first-class CLI contracts [S9][S10]; a sync tool without them is not scriptable.
- **Do not ship continuous mode.** Syncthing and Unison both show that continuous sync makes conflict timing and partial writes someone else's problem. The user chose manual; keep conflict resolution deterministic [S4][S6].
- **Encryption and LFS are separate products.** git-crypt and sops show the key-management cost; git-lfs shows the server/client cost [S15][S16][S17]. Both are explicitly out of scope for <100 MB private notes.

## Feature Dependencies

```text
[Root-commit identity] ──requires──> [Non-shallow, non-replace revision walk]

[init] ──requires──> [Root-commit identity] + [Pollution guard] + [Stealth exclude writer] + [Store bootstrap]

[Base state: last synced commit]
        │
[Three-way classifier] ──requires──> [Base state] + [Content hashing]
        │
        ├──> [status]
        ├──> [push]
        └──> [pull]

[push] ──requires──> [Three-way classifier] + [Tree builder] + [Remote fetch]
[Tree builder] ──requires──> [Ignore rules] + [Byte-exact blobs]

[pull] ──requires──> [Three-way classifier] + [Deletion rules] + [Atomic writer]
[Conflict copies] ──requires──> [Machine identity] + [Three-way classifier] + [Artifact ignore]
[push --resolve] ──requires──> [Conflict copies] + [Base state]

[log] ──requires──> [Mirror history] + [Project prefix]
[restore] ──requires──> [log/history] + [Blob reader] + [Path safety]

[--dry-run] ──enhances──> [init] [push] [pull] [restore]
[Shared-store prefix splicing] ──enhances──> [push]

[Auto text merge] ──conflicts──> [Local-wins conflict guarantee]
[Hidden refs / worktree in team repo] ──conflicts──> [Zero-pollution guarantee]
[Watch/daemon] ──conflicts──> [Manual, deterministic sync]
[Force push] ──conflicts──> [Append-only history and restore]
```

### Dependency Notes

- **`init` requires identity, guard, exclude, and store bootstrap:** adoption decisions depend on knowing the project ID and whether the remote prefix already exists. The tracked-file check runs before the exclude write, so `init` never edits `.git/info/exclude` in a repo where docs are already tracked; the ignore check runs immediately after the write and before any sync command.
- **`status`, `push`, and `pull` require the same classifier:** if each command re-implemented base/local/remote comparison, they would disagree, which is worse than being wrong consistently. One classifier is the correctness core; the commands are presentation and I/O around it.
- **`pull` requires deletion rules and atomic writes:** applying a remote delete is the only operation that destroys a local file, so it must be gated by "the other side did not change it." Each file is replaced atomically; a failed transfer must leave every written file intact (no torn writes), must not advance base, and must be safely re-runnable. A multi-file pull may be partially applied; it is never half-written per file.
- **Conflict copies require machine identity and artifact ignore:** without identity, two machines produce colliding filenames; without the artifact ignore, the next `push` would commit the conflict copy into the private repo and propagate it to every machine.
- **`push --resolve` requires conflict state:** it must only accept paths currently reported as conflicted; otherwise it becomes a general force flag that can erase remote changes. Resolution is a state transition, not a content transform.
- **Shared-store splicing enhances `push`:** it is what turns "a private repo per project" into "one private repo"; it requires a top-level tree splice and a fetch/retry loop, but not a merge engine, because project prefixes never overlap.
- **`restore` requires `log`/history and path safety:** restoring a deleted path from a historical commit is a tree read, but the target path must be re-validated as relative, inside the docs directory, and not a symlink.

## MVP Definition

### Launch With (v1)

The v1 cut line is: **every path that can write or delete a document is guarded, classified three ways, and fails loudly.** Anything that does not serve that sentence is deferred.

- [ ] `init` — remote binding, project ID derivation/override, store bootstrap, adoption of an existing remote prefix, idempotent re-init
- [ ] Stealth exclude + pre-write guard — docs untracked, ignored, project ID matched, no symlinked docs dir, no tracked docs
- [ ] `status` — clean/ahead/behind/diverged, per-file A/M/D/C, content-hash comparison, `--offline` and fetch timestamp
- [ ] `push` — own-prefix subtree, splice onto fetched tip, append-only commit, no-op detection, retry on race, refuse when behind or on rewritten base history
- [ ] `pull` — clean add/modify/delete application, atomic writes, conflicts keep local, base advances only when clean and all writes succeed
- [ ] `log` — project-scoped sync history with machine trailer and path filter
- [ ] `restore` — single file from any mirror commit, overwrite refusal without `--force`
- [ ] Conflict copies — `<name>.theirs.<machine>`, ignored from sync, non-zero exit, `push --resolve <path>` for local-wins
- [ ] Deletion propagation — clean deletes both directions, modify/delete conflicts never erase
- [ ] Machine identity — config, commit trailer, conflict suffix
- [ ] `--dry-run` and the stable exit-code contract
- [ ] Built-in junk ignore list + `*.theirs.*` + `.git/`
- [ ] Byte-exact blobs, exec-bit preservation, symlink refusal, 50/100 MB warning/refusal
- [ ] Path safety and single-instance lock — traversal/symlink/Windows-name rejection, case-collision refusal
- [ ] Cross-platform behavior — path separators, case rules, Windows replace semantics, CRLF, tested on Linux/macOS/Windows
- [ ] Multi-machine flow — second-machine `init`/adoption path documented and exercised end to end
- [ ] Any git remote with system credentials; credential-in-URL warning
- [ ] `--help` / `--version`, non-interactive by default

### Add After Validation (v1.x)

Add when the core loop has real multi-machine use, not before.

- [ ] `.docignore` at the docs root, gitignore syntax, synced as a normal file — trigger: first user who needs to exclude a generated file beyond the built-ins
- [ ] `--json` for `status`, `log`, `push`, `pull` — trigger: first script/alias integration
- [ ] `doctor` — re-run guard, verify exclude block, store reachability, base/remote consistency, orphaned temp files — trigger: first "why is it refusing?" support question
- [ ] Shell completion — trigger: command surface stabilizes
- [ ] Symlink support (POSIX first, explicit Windows behavior) — trigger: first user with symlinked notes
- [ ] NFD/NFC normalization for macOS filenames — trigger: first macOS user with decomposed Unicode names
- [ ] `restore --stdout` and `--to -` — trigger: diff/merge workflows
- [ ] Progress indicator for large pulls/pushes — trigger: first >100 MB docs tree
- [ ] `push -m/--message` — trigger: users wanting meaningful sync messages
- [ ] `status --all-projects`, `log --all` — trigger: shared-store users managing several projects

### Future Consideration (v2+)

Defer until the single-project loop has proven itself.

- [ ] `store gc` / mirror pruning policy — mirror is a full clone per project; growth is real but not a v1 correctness issue
- [ ] Secondary remote replication (backup remote) — requires per-remote state and failure semantics; v1's private host is the backup
- [ ] `verify` / fsck of mirror and remote — useful after years, not before adoption
- [ ] Cross-project inventory (`store list`) — read-only convenience for shared-store users

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| `init` + adoption | HIGH | MEDIUM | P1 |
| Stealth exclude writer | HIGH | LOW | P1 |
| Pre-write pollution guard | HIGH | MEDIUM | P1 |
| `status` (divergence + per-file) | HIGH | MEDIUM | P1 |
| `push` | HIGH | MEDIUM | P1 |
| `pull` (three-way engine) | HIGH | HIGH | P1 |
| `log` | MEDIUM | LOW | P1 |
| `restore` | HIGH | MEDIUM | P1 |
| Conflict copies + `push --resolve` | HIGH | MEDIUM | P1 |
| Deletion propagation | HIGH | MEDIUM | P1 |
| Atomic apply + lock | HIGH | MEDIUM | P1 |
| Built-in ignore list | MEDIUM | LOW | P1 |
| `--dry-run` | HIGH | LOW | P1 |
| Stable exit codes | HIGH | LOW | P1 |
| Machine identity | MEDIUM | LOW | P1 |
| Byte-exact fidelity + size guard | HIGH | LOW | P1 |
| Path safety | HIGH | MEDIUM | P1 |
| Any remote + system credentials | HIGH | LOW | P1 |
| Offline inspection | MEDIUM | LOW | P1 |
| Cross-platform behavior (Windows/macOS/Linux) | HIGH | MEDIUM | P1 |
| Multi-machine onboarding | HIGH | LOW | P1 |
| CLI ergonomics (help, version, non-interactive) | MEDIUM | LOW | P1 |
| `.docignore` | MEDIUM | LOW | P2 |
| JSON output | MEDIUM | LOW | P2 |
| `doctor` | MEDIUM | LOW | P2 |
| Shell completion | LOW | LOW | P2 |
| Symlink support | MEDIUM | MEDIUM | P2 |
| Case-collision detection | HIGH | MEDIUM | P1 |
| NFD/NFC normalization | MEDIUM | MEDIUM | P2 |
| `restore --stdout` | LOW | LOW | P2 |
| Progress indicator | LOW | LOW | P2 |
| `push -m` | LOW | LOW | P2 |
| `status --all-projects` / `log --all` | MEDIUM | LOW | P2 |
| `store gc` | LOW | MEDIUM | P3 |
| Secondary remote | MEDIUM | HIGH | P3 |
| `verify` | MEDIUM | MEDIUM | P3 |

**Priority key:** P1 = must ship for launch; P2 = add when the core is stable; P3 = future. **Cost mapping:** HIGH = L, MEDIUM = M, LOW = S.

**Scope cut order if schedule slips:** cut `log --path` → `restore --to <dir>` (keep default in-place restore) → per-file detail for clean states (keep summary and all non-clean detail) → size-threshold configurability (keep defaults). **Never cut:** pollution guard, three-way classifier, deletion rules, conflict copies, atomic writes, or the base-advance-only-when-clean-and-complete rule. Those are the product.

## Competitor Feature Analysis

| Capability | Syncthing | Unison | rclone (bisync) | git-annex | Hand-rolled bare repo | doc-connector |
|------------|-----------|--------|-----------------|-----------|-----------------------|---------------|
| Data scoped per project | Folder-level | Root-pair profile | Remote path | Repo/folder | One repo per project | One prefix in a shared private repo |
| History and restore | Optional file versioning, not git | No | No (backup-dir only) | Git metadata; content depends on copies | Full git | Full git, project-filtered `log`/`restore` |
| Conflict preservation | Renamed `sync-conflict` copies | Displayed; optional copies | Conflict policy, optional backup dir | Annex-specific | Manual git merge | Local wins + `<name>.theirs.<machine>` |
| No background process | No (continuous) | Optional `-repeat` | No (invoked) | No (invoked; assistant optional) | Yes | Yes |
| Team repo untouched | Yes (non-git folder) | Yes (non-git) | Yes (non-git) | Yes (separate repo) | Yes (separate repo) | Yes, by enforced design |
| Deterministic project identity | None (device/folder IDs) | None (paths/profiles) | None (remote path) | None (repo UUID) | None (or root SHA by hand) | Root commit SHA, 12 hex |
| Multiple projects, one remote | One folder each | One profile each | One remote/path each | One annex per repo | No | Yes, `<project_id>/` prefixes |
| Plain files readable by plain git | No | No | No | No | Yes | Yes |

## Sources

- [S1] git-annex homepage — https://git-annex.branchable.com/ (v10.20261020, checked 2026-10-22)
- [S2] git-annex, "how it works" — https://git-annex.branchable.com/how_it_works/
- [S3] git-annex, "what git-annex is not" — https://git-annex.branchable.com/not/
- [S4] Syncthing, "Understanding Synchronization" — https://docs.syncthing.net/users/syncing.html (docs v2.1.0)
- [S5] Syncthing, "Ignoring Files" — https://docs.syncthing.net/users/ignoring.html
- [S6] Unison README — https://github.com/bcpierce00/unison
- [S7] Unison manual (conflicts, archives, `copyonconflict`) — https://raw.githubusercontent.com/bcpierce00/unison/documentation/unison-manual.html
- [S8] unison(1) man page — https://man.archlinux.org/man/unison.1.en
- [S9] rclone sync — https://rclone.org/commands/rclone_sync/
- [S10] rclone documentation, exit codes — https://rclone.org/docs/#exit-code
- [S11] chezmoi homepage — https://www.chezmoi.io/ (v2.73.3)
- [S12] chezmoi comparison table — https://www.chezmoi.io/comparison-table/
- [S13] yadm overview — https://yadm.io/docs/overview
- [S14] yadm encryption — https://yadm.io/docs/encryption
- [S15] git-crypt README — https://github.com/AGWA/git-crypt (v0.8.0)
- [S16] sops README — https://github.com/getsops/sops
- [S17] Git LFS — https://git-lfs.com/ (v3.8.0)
- [S18] git-worktree documentation — https://git-scm.com/docs/git-worktree
- [S19] git-sparse-checkout documentation — https://git-scm.com/docs/git-sparse-checkout
- [S20] gitignore documentation (`$GIT_COMMON_DIR/info/exclude`, precedence) — https://git-scm.com/docs/gitignore
- [S21] git-push documentation — https://git-scm.com/docs/git-push
- [S22] git-hash-object documentation (`--no-filters`) — https://git-scm.com/docs/git-hash-object
- [S23] git-rev-list documentation (`--max-parents=0`) — https://git-scm.com/docs/git-rev-list
- [S24] git-commit-tree documentation — https://git-scm.com/docs/git-commit-tree
- [S25] git-check-ignore documentation (exit status) — https://git-scm.com/docs/git-check-ignore
- [S26] git-update-index documentation (`--cacheinfo`, `GIT_INDEX_FILE`) — https://git-scm.com/docs/git-update-index
- [S27] git-rev-parse documentation (`--is-shallow-repository`) — https://git-scm.com/docs/git-rev-parse
- [S28] git-cat-file documentation (blob reads) — https://git-scm.com/docs/git-cat-file
- [S29] git documentation (`--no-replace-objects`) — https://git-scm.com/docs/git#Documentation/git.txt---no-replace-objects
- [S30] git-mktree documentation (top-level tree splice) — https://git-scm.com/docs/git-mktree
- [S31] Project decisions and constraints — `/mnt/c/Home/Repos/doc-connector/.planning/PROJECT.md`
- [S32] git-rev-parse 2.30.0 documentation (no `--path-format`) — https://git-scm.com/docs/git-rev-parse/2.30.0
- [S33] git-rev-parse 2.31.0 documentation (`--path-format` introduced) — https://git-scm.com/docs/git-rev-parse/2.31.0
- [S34] Architecture research, "Project identity: exact derivation" — `/mnt/c/Home/Repos/doc-connector/.planning/research/ARCHITECTURE.md`

---

*Feature research for: private project-scoped document sync over git*
*Researched: 2026-10-22*
