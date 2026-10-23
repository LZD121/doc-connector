# doc-connector

Sync a project's **private documentation** to **your own private git repository**
while the team repository stays untouched.

Your notes live as ordinary files in a subdirectory of the project worktree
(`private-docs/` by default), but they are excluded through `.git/info/exclude`,
so they never appear in the team repository's history, branches, or `git status`.
The docs are versioned in a separate private repository, under one deterministic
`<project_id>/` subtree per project — many projects can share one private remote.

```text
team repo (read-only for this tool)          your private repo
├── src/                                     ├── 743c420a8e7b/   ← this project
├── README.md                                │   ├── architecture.md
└── private-docs/  ← git never sees these     │   └── meetings/2026-10.md
    ├── architecture.md                      └── 91bd2c77af03/   ← another project
    └── meetings/2026-10.md
```

## Why

Team repositories are not for personal notes. The usual alternatives are worse:
a second clone you forget to update, a synced folder without history, or a
`.gitignore` edit you cannot make in someone else's repo. `doc-connector` keeps
two tracks: code through the team repository, private documents through yours.

## Install

```bash
# Homebrew (macOS, Linux)
brew install --cask LZD121/tap/doc-connector

# from source (Go 1.26+)
go install github.com/LZD121/doc-connector@latest
```

Release archives for linux/darwin/windows on amd64/arm64 are attached to every
tag, together with `checksums.txt`. Windows and Scoop/winget manifests are
generated at release time; the Scoop bucket and winget PR are opt-in (they need
a token), so use the archive on Windows for now.

Those archives are **not code-signed**. macOS users who download the zip with a
browser will meet Gatekeeper; the Homebrew cask is unaffected.

Requirements: **git >= 2.30** on `PATH`, and Go 1.27-built binaries need macOS 13+.

## Quick start

```bash
# 1. In the project (the team repository), once:
doc-connector init --remote git@github.com:you/private-docs.git

# 2. Write notes in private-docs/, then:
doc-connector status      # clean / ahead / behind / diverged, per file
doc-connector push        # publish this project's subtree

# 3. On another machine: clone the team repo first, then:
doc-connector init --remote git@github.com:you/private-docs.git --machine laptop
doc-connector pull
```

Configuration is hidden and **does not travel with the clone**
(`.git/doc-connector/config.yaml`), so every machine runs `init` once — that is
the price of never committing tool state.

## Commands

| Command | What it does |
|---|---|
| `init --remote <url>` | Bind this repository, derive the project id, write the exclude block |
| `status` | Three-way comparison (local / baseline / remote) with per-file A/M/D/C |
| `push` | Publish `<project_id>/`; refuses to overwrite anything this machine has not seen |
| `pull` | Apply remote changes atomically; conflicts keep local bytes and write `<name>.theirs.<machine>` |
| `log` | Sync history from the local mirror, offline |
| `restore <commit> [path]` | Write a historical version back; refuses to overwrite unsynced work without `--force` |
| `diff <commit> [path]` | Diff a historical version against the working tree, offline |
| `doctor` | Read-only diagnosis: git version, exclude block, tracked docs, config/state, lock, remote tip, leftovers |
| `status --root <dir>` | All initialized repositories under a directory, one line each |
| `sync --root <dir>` | Pull what is behind, push what is ahead, report what is blocked |
| `tui --root <dir>` | Interactive workspace screen with conflict triage |
| `completion <shell>` | Completion script for bash, zsh or fish |

Most commands accept `--json`. stdout carries exactly one JSON document per run
(with `schema_version`, `command`, `ok`, and on failure `error.code`,
`error.message`, `error.hint`); diagnostics go to stderr.

`sync --root` is deliberately conservative: it runs one repository at a time,
re-fetches before deciding, and marks a diverged repository `blocked` — it never
resolves a conflict for you.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success, including "already up to date" and "nothing to push" |
| 1 | Conflict or decision required (write paths, blocked repositories) |
| 2 | Usage, identity or environment error |
| 3 | Isolation failure (tracked docs, unsafe path, lock held) |
| 4 | Network or authentication failure |
| 130 | Interrupted |

## Safety model

- The team repository is **read-only**. The only writes are `.git/info/exclude`
  (local, untracked) and `.git/doc-connector/` (config, state, mirror, lock).
  No commits, no index changes, no `.gitignore` edits.
- Documents live once, in the worktree. The private repository is mirrored as a
  bare repo under `.git/doc-connector/store.git`; no second worktree exists.
- `push` never force-pushes and refuses to clobber a remote version this machine
  has not seen. `pull` never deletes local-only files. Conflict copies
  (`.theirs.<machine>`) are never synced.
- Write commands (`init`, `push`, `pull`) hold one cross-process lock per project.
- Junk files (`.DS_Store`, `Thumbs.db`, …), symlinks (unless `--follow-symlinks`),
  case/NFC collisions and files over 100 MiB are refused with an actionable message;
  `--force` accepts files over 50 MiB and `--allow-large` over 100 MiB, explicitly.
- The tool stores no secrets: it shells out to `git` and reuses your SSH agent,
  credential helper and proxy. Do not put secrets in the docs directory — the
  private repository ACL is the security boundary, not encryption.

## Configuration

`.git/doc-connector/config.yaml` (mode 0600, hand-editable, unknown keys are
preserved). Schema 2:

```yaml
schema: 2
remote: git@github.com:you/private-docs.git
branch: main
machine: laptop
docs_dir: private-docs          # mirror of docs_dirs[0].path
docs_dirs:
  - path: private-docs          # prefix "" → <project_id>/<file>
  - path: notes                 # prefix notes → <project_id>/notes/<file>
    prefix: notes
project:
  id: 743c420a8e7b              # lower(root commit)[:12], write-once
  id_source: derived
  root_commit: 743c420a8e7b11c3382c49dfec71987cb7a6f710
```

Each mapped directory may contain a `.docignore` (not synced) with basename
patterns (`*.tmp`) and directory patterns (`drafts/`). The built-in junk list
cannot be overridden.

## What this is not

No daemon or watch mode, no encryption, no secret storage, no automatic text
merge, no Git LFS, no GUI, no automatic remote creation. Those decisions and
their reasons are recorded in `.planning/milestones/*` — they are product
choices, not missing features.

## License

MIT — see [LICENSE](LICENSE).
