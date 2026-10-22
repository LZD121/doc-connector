# Pitfalls Research

**Domain:** project-scoped private document synchronization over a shared personal git repository (Go CLI, plumbing-based push/pull, `.git/info/exclude` isolation)
**Researched:** 2026-10-22
**Confidence:** HIGH for git mechanics and platform behavior (empirically verified on `git 2.47.3` under WSL2/9p and against primary docs); MEDIUM for phase sequencing (no ROADMAP.md existed when this was written)

## How to read this

Every pitfall below is bound to this exact mechanism: a project directory excluded via `.git/info/exclude`, a `<project_id>/` prefix derived from the main repo's root commit, a bare mirror at `.git/doc-connector/store.git`, and a three-way sync against a stored baseline commit. Generic advice ("write tests", "handle errors") is deliberately absent.

Phase names referenced throughout (they are the natural decomposition of the requirements, not a committed roadmap):

- **P1 — Identity & init:** root-commit resolution, `project_id`, config bootstrap, store creation.
- **P2 — Isolation guard:** docs directory, `.git/info/exclude`, preflight refusals, tracked-file detection.
- **P3 — Scan & push:** walker, junk list, tree building, commit creation, push, baseline advance.
- **P4 — Pull & conflicts:** three-way classification, conflict artifacts, atomic apply, journal.
- **P5 — Inspect & recover:** `status`, `log`, `restore`.
- **P6 — Cross-platform & network hardening:** Windows/macOS portability, credentials, proxies, packaging.

`§Verified` markers point at the appendix, which records the exact command and observed output behind a claim.

---

## Critical Pitfalls

### Pitfall 1: A shallow clone silently redefines the project identity

**What goes wrong:**
On a `--depth 1` clone, `git rev-list --max-parents=0 HEAD` returns the *tip* commit, not the true root. `doc-connector init` computes a different 12-hex `project_id`, pushes docs into a second `<project_id>/` folder, and the user now has two divergent copies in the private repo with no error anywhere. (§Verified)

**Why it happens:**
Shallow clones have a graft boundary: the boundary commit has no parents, so it *is* a root as far as revision walking is concerned. CI checkouts (`actions/checkout` defaults to `fetch-depth: 1`), `git clone --depth`, and `git fetch --depth` all produce this state. `--unshallow` is not always possible (no network, or the clone was made with grafts).

**How to avoid:**
- Preflight every command (not just `init`) with `git rev-parse --is-shallow-repository`; refuse on `true` with a message naming `git fetch --unshallow` and `--project-id`.
- Also refuse when `<git-dir>/shallow` exists even if the flag misreports (older git).
- If `--project-id` is supplied explicitly, allow operation on a shallow clone — the user has supplied the missing fact.
- Never auto-unshallow: it can download gigabytes behind the user's back.

**Warning signs:**
`init` "succeeds" quickly on a repo that was just cloned by CI; `git log --oneline | wc -l` returns 1; the printed project id changes after `git fetch --unshallow`.

**Detection test:**
Fixture with 3 commits; `git clone --depth 1 file://...`; run `init`; assert non-zero exit, stderr contains `shallow`, and no config file was written. Then run with `--project-id <expected>` and assert success. (§Verified: depth-1 root `2079a5c…` vs true root `bdaab60…`.)

**Phase to address:**
P1 — Identity & init. Enforced again as a cheap preflight in P3.

---

### Pitfall 2: Repository resolution fails or resolves to the wrong repository

**What goes wrong:**
Four distinct environments are mistaken for "a project":
1. A source archive (`Download ZIP`) has no `.git` at all — every git call exits 128.
2. A linked worktree or submodule has `.git` as a *file*, so code that joins `root + "/.git/doc-connector"` fails with `ENOTDIR`.
3. A submodule resolves to the submodule's own repository, producing the submodule's root commit rather than the project the user thinks they are in.
4. A bare repo (or `GIT_DIR` exported by a shell/hook) resolves `--show-toplevel` to nothing, and the docs directory has no meaningful location.

**Why it happens:**
`git rev-parse --git-dir` is the obvious call, but it returns `.git`, a relative path, or a gitfile path depending on cwd and env. Developers test only on a normal clone.

**How to avoid:**
- Resolve with `git -C <abs cwd> rev-parse --absolute-git-dir --git-common-dir --show-toplevel --is-inside-work-tree --is-bare-repository` in one call. Note: `--path-format=absolute` only exists from git 2.31 (§Verified: absent from the 2.30 docs), so with a 2.30 floor you must absolutize `--git-common-dir`/`--show-toplevel` yourself against the cwd.
- Require `--is-inside-work-tree true` and `--is-bare-repository false`; error on the archive case with "no .git directory; clone the repository instead of downloading a ZIP".
- If `--show-superproject-working-tree` is non-empty, refuse unless `--project-id` is given (the user must state which repository owns the docs).
- Strip `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`, `GIT_COMMON_DIR` from every child environment; a hook-invoked `doc-connector` otherwise operates on the wrong repo.

**Warning signs:**
Config written under `.git` that is a 100-byte text file; `--show-toplevel` empty; `init` succeeding inside `vendor/some-lib`.

**Detection test:**
Table-driven integration test: plain clone, `git worktree add`, `git submodule add`, `git clone --bare`, unpacked `git archive` tarball. Assert exactly one succeeds without flags and the others fail with distinct, named errors.

**Phase to address:**
P1 — Identity & init.

---

### Pitfall 3: Multiple root commits make the identity ambiguous, and code picks one arbitrarily

**What goes wrong:**
Repositories that merged unrelated histories (`git merge --allow-unrelated-histories`), imported a vendor drop, or carry an ancient orphan branch have more than one root. `git rev-list --max-parents=0 HEAD` prints two SHAs; "take the first line" yields an identity that depends on commit dates and traversal order, so two clones can disagree. (§Verified: the order of the two roots changed between default and `--reverse --date-order` output.)

**Why it happens:**
`rev-list` output order is date-driven and not a stable contract; most scripts assume a single line. Replace refs and `.git/info/grafts` can also rewrite ancestry so the "root" is not the historical first commit.

**How to avoid:**
- Compute roots with `GIT_NO_REPLACE_OBJECTS=1` over an explicit rev set (`HEAD` and, separately, `--all`).
- If the set for `HEAD` has more than one element: hard error, print all roots with the branches that reach them, require `--project-id`.
- If `HEAD` has one root but `--all` has more: warn (not error) naming the extra roots and branches — this is the early signal of Pitfall 4.
- Never sort roots and take `[0]`; never dedupe by prefix.

**Warning signs:**
Two SHAs printed where the code expects one; the same repo yielding different ids on two machines that fetched different branches; `git replace -l` non-empty.

**Detection test:**
Fixture built with `git checkout --orphan other && git merge --allow-unrelated-histories`; assert `init` exits with code 2 and lists both roots; assert `init --project-id <12hex>` succeeds.

**Phase to address:**
P1 — Identity & init.

---

### Pitfall 4: Identity depends on which branch happens to be checked out

**What goes wrong:**
The user runs `init` while on `gh-pages`, a release branch, or a detached HEAD from an old PR. The root of that history differs from the root of the default branch, producing a different `project_id` than the same repo on another machine that sits on `main`. Docs split across prefixes; `status` on the second machine reports "clean" while pointing at an empty folder.

**Why it happens:**
Root-of-`HEAD` is the simplest implementation and works for single-history repos, which is what developers test with. Orphan branches are common (`gh-pages`, `docs`, vendor snapshots) and clones differ in which refs they have fetched.

**How to avoid:**
- Prefer the root of `refs/remotes/origin/HEAD` when it resolves; fall back to `HEAD`; if both resolve and disagree, error.
- Print the resolution source (`HEAD` vs `origin/HEAD` vs `--project-id`) and the full root SHA on `init` and in `status --verbose` so two machines can be compared by eye.
- Store `root_commit` and `project_id` in config; on every later run assert the recorded root is still the resolved root (see Pitfall 5).

**Warning signs:**
Different `project_id` on two machines for the same repo; `init` run on a branch whose name suggests documentation or releases; `origin/HEAD` unset (`git remote set-head origin -a` never run).

**Detection test:**
Create an orphan `gh-pages` branch; check it out; run `init`; assert it errors ("ambiguous project identity; run from the default branch or pass --project-id") rather than silently emitting a second id.

**Phase to address:**
P1 — Identity & init.

---

### Pitfall 5: Squashed or re-created history changes the identity and orphans the old prefix

**What goes wrong:**
A maintainer squashes the repository's history (or the project is re-initialized, or moved to a new repo via "Import" with a fresh initial commit). The root SHA changes. Without `--project-id`, the next `push` writes into a brand-new prefix; the old prefix keeps a full history nobody updates, and `pull` on the new prefix appears to have "lost" all documents.

**Why it happens:**
The design's identity is content-addressed on the very first commit — the most immutable-looking object that is, in fact, rewritten by any history rewrite, including the common `git checkout --orphan && git commit` "reset history" recipe.

**How to avoid:**
- On `init` with an existing config: if the newly computed root differs from `config.root_commit`, refuse and print both ids plus the exact remediation (`--project-id <old-id>` to keep syncing, or `--reset-identity` to deliberately start a new prefix).
- `status` should show the recorded root short SHA, so a changed root is visible before a write.
- Document the squash caveat in `init --help`; make the id part of the success output.

**Warning signs:**
`git log --oneline | tail -1` shows a recent date; config exists but `root_commit` no longer resolves; `status` says "behind by N" against an empty remote prefix.

**Detection test:**
Init on fixture A, then re-create the repo with a single fresh commit in place; assert `init` and `push` both fail with "identity changed", and that `--project-id <old>` restores the original prefix.

**Phase to address:**
P1 — Identity & init (detection); P5 must surface the drift in `status`.

---

### Pitfall 6: Two different projects share a root commit and therefore a prefix

**What goes wrong:**
Forks, "Import repository", and internal starter templates preserve the original root commit. Two genuinely different projects — same user, same private docs repo — derive the same `project_id` and interleave their documents in one folder. Names collide, deletes from one project propagate to the other, and the three-way merge produces conflicts between unrelated content.

**Why it happens:**
Root-commit identity is only as unique as the commit graph's origin. The design intentionally cannot use `.git` paths or local UUIDs (they differ per machine), so the collision has to be handled out-of-band.

**How to avoid:**
- Write an identity record into the private repo on first push, *outside* the documents namespace: `.doc-connector/projects/<project_id>.json` containing `{"root_commit", "remote_url_normalized", "created_by_host", "created_at", "schema": 1}`.
- Before every push/pull, read it: if `root_commit` matches but `remote_url_normalized` differs, require `--project-id` or `--accept-remote-change` (fork vs renamed repo is undecidable).
- Normalize URLs for comparison: lowercase scheme/host, strip credentials, strip trailing `.git` and `/`.
- Keep the metadata file in the same commit as the first documents push so it cannot be missing while documents exist.

**Warning signs:**
`<project_id>/` already exists remotely but `.doc-connector/projects/<id>.json` is absent; documents in the folder that the user does not recognize; `log` authors from another machine the user never used.

**Detection test:**
Fork fixture: `git clone --mirror` of repo A, rewrite nothing, treat as repo B. `init` both against one private repo; assert the second errors and prints both remote URLs.

**Phase to address:**
P1 (schema/validation) and P3 (atomic first write of the record).

---

### Pitfall 7: Config and store land in a per-worktree gitdir, and two worktrees fight over one baseline

**What goes wrong:**
`git rev-parse --absolute-git-dir` inside a linked worktree returns `.git/worktrees/<name>` (§Verified). Config and `store.git` written there are invisible from the main checkout: the user re-inits on every worktree, creates a second full mirror, and each copy keeps its own baseline. Both worktrees contain `private-docs/`, with different files, against one remote prefix — so whichever pushes second either conflicts or overwrites.

**Why it happens:**
"`.git` is a directory" is true for the main worktree only. `--git-path info/exclude` already resolves to the *common* `.git/info/exclude` (§Verified), so exclusion works while config silently does not — an inconsistency that hides the bug.

**How to avoid:**
- Resolve `--git-common-dir` for config, store, journal, lock, and conflicts file; use `--git-path` for anything git defines (exclude).
- Record `worktree` (absolute top-level) in config. If the docs directory exists in more than one entry of `git worktree list --porcelain`, refuse to push and explain: one repository, one docs directory.
- If the config's `worktree` differs from the current top-level, allow read-only `status`/`log` but refuse `push`/`pull` unless `--allow-worktree`.

**Warning signs:**
Two `store.git` directories under different `.git/worktrees/*`; `init` succeeding twice in the same repository; `git worktree list` showing multiple checkouts with `private-docs/`.

**Detection test:**
`git worktree add ../wt`; run `init` in the main checkout; assert no `.git/worktrees/wt/doc-connector` exists; create `private-docs/` in both worktrees and assert `push` fails with the ambiguity message.

**Phase to address:**
P1 (path resolution, worktree registry) and P2 (guard).

---

### Pitfall 8: A `.gitignore` negation overrides `.git/info/exclude` and exposes the docs directory

**What goes wrong:**
The team repository's `.gitignore` contains `!private-docs/` (or a broad `*` + `!*/` idiom). Ignore precedence is command line > `.gitignore` > `.git/info/exclude` > `core.excludesFile`, so the negation wins: `git status` now shows `?? private-docs/`, `git add -A` stages the documents, and the next team commit leaks them. (§Verified: `git status --porcelain` printed `?? private-docs/`.)

**Why it happens:**
Everyone believes `.git/info/exclude` is "extra ignores". It is a *lower-precedence* source, not an override. The docs also never test against a repo whose `.gitignore` negates the path.

**How to avoid:**
- The guard must not ask "is it ignored?" but "is it ignored *by our rule*": run `git check-ignore -v --no-index` on the directory and on a sample file, and require the reported source to be exactly the resolved `.git/info/exclude` path.
- If the deciding source is a `.gitignore`, refuse all write commands, print the file, line, and pattern, and explain that only the repository owner can change that file.
- Never modify `.gitignore` (explicitly out of bounds) and never add the path to a tracked file as a workaround.

**Warning signs:**
`git status` showing anything under the docs directory; `check-ignore -v` naming `.gitignore`; a `*` + `!*/` pattern pair in the team repo.

**Detection test:**
Fixture whose `.gitignore` contains `!private-docs/`; assert `status` exits 3 with the pattern and line, and that `push`/`pull` refuse before touching network or disk.

**Phase to address:**
P2 — Isolation guard. This is the highest-severity isolation bug in the design; it belongs in the first guard commit.

---

### Pitfall 9: `check-ignore` is index-aware and its exit codes are misread

**What goes wrong:**
Three independent misreadings:
1. For a *tracked* file, default `git check-ignore` prints nothing and exits **1** even though the pattern matches, because ignore rules do not apply to tracked files. A guard built on it concludes "not ignored" (correctly) but for the wrong reason, and a guard that treats 1 as fatal blocks all normal operation.
2. Exit **128** (fatal: not a repository, bad path) is treated as "not ignored" by a `rc != 0` check, so the guard passes while the repository is unreadable.
3. With several pathnames, exit 0 means *at least one* path is ignored — the per-path verdict is lost unless `-v -n -z` is used.
(§Verified for 1: tracked file → rc 1; `--no-index` → rc 0.)

**Why it happens:**
The command reads like a boolean predicate (`check-ignore path`), and wrapper code naturally maps `err != nil` to "not ignored" or "fatal". The three exit codes, the index awareness, and the "any path matched" aggregation are documented in separate paragraphs that are easy to miss, and every happy-path test uses untracked, ASCII, single paths inside a valid repository.

**How to avoid:**
- Use `git check-ignore -v -n -z --no-index --stdin` and parse 4-field NUL records (`source\0linenum\0pattern\0pathname\0`); empty first fields mean "no match".
- Treat 0 = matched, 1 = not matched, 128 = hard failure; never collapse them.
- Always pass `--no-index` for the guard, and separately use `git ls-files -z -- <docs>/` to detect the tracked case (Pitfall 12).

**Warning signs:**
A guard that passes after `git add -f private-docs/x.md`; a guard that fails when invoked from a subdirectory; logic that checks `err != nil` from a git wrapper for a boolean.

**Detection test:**
Table test: untracked ignored file, tracked file, non-ignored file, path outside repo. Assert `(ignored, source, rc)` for each; assert the tracked case is reported by `ls-files`, not by `check-ignore`.

**Phase to address:**
P2 — Isolation guard.

---

### Pitfall 10: The exclude pattern is anchored wrong, or stops matching after a rename

**What goes wrong:**
- `private-docs/` (no leading slash) matches at any depth and hides an unrelated `sub/private-docs/` owned by the team (§Verified).
- `/private-docs/` stops matching if the directory is renamed — and a *case-only* rename is invisible on Windows/macOS: with `core.ignorecase=true` git's exclude matching is case-insensitive, so `Private-Docs/` is still excluded there but **not** on Linux (§Verified: pattern matched with `ignorecase=true`, rc 1 with `false`).
- The trailing slash makes the pattern directory-only; if the path is ever replaced by a file, nothing is excluded.

**Why it happens:**
The anchoring rule ("a slash anywhere except the end anchors the pattern") is subtle and most examples people copy are unanchored file globs. Exclude matching also silently inherits `core.ignorecase`, which is set per clone, so the same pattern is evaluated with different semantics on different machines — and nobody tests the rename case because the directory "never moves".

**How to avoid:**
- Write exactly one anchored line, `/private-docs/`, derived from the configured relative path, and keep the configured path normalized (`filepath.ToSlash`, no `./`, no trailing slash).
- On every run, re-run the guard against the *current* on-disk name of the docs directory (readdir + case-insensitive comparison), so a case-only rename is detected instead of silently unexcluded.
- If config's `docs_dir` differs from the default, treat the exclude line as a function of it and rewrite idempotently.
- Detect a file-vs-directory change and re-write the pattern without the trailing slash only if the user confirms.

**Warning signs:**
Two directories matching the docs name; `git check-ignore -v` showing the pattern but `git status` listing the directory; a repository that behaves differently on Windows and Linux.

**Detection test:**
Fixture with `sub/private-docs/x.md`; assert the anchored pattern does not ignore it. Then rename to `Private-Docs` on a `core.ignorecase=true` fixture and run the guard from a simulated Linux config; assert it reports "docs directory renamed; expected `private-docs`".

**Phase to address:**
P2 — Isolation guard.

---

### Pitfall 11: Appending to `.git/info/exclude` corrupts the previous last pattern

**What goes wrong:**
The file may not exist, may not end with a newline, and may contain CRLF or user patterns. A naive `os.OpenFile(..., O_APPEND)` + `WriteString("/private-docs/\n")` turns a previous `build/` into `build//private-docs/`, silently un-ignoring `build/` for the whole team checkout. Repeated `init` runs append duplicates.

**Why it happens:**
`info/exclude` looks like an append-only log, but gitignore syntax is line-oriented and a missing final newline is legal. Developers test with a file they just created.

**How to avoid:**
- Read the file, split on `\n`, trim trailing `\r` for comparison, and check for an exact-line match first (idempotent).
- If the file is non-empty and its last byte is not `\n`, insert one before appending.
- Write via temp file in the same directory + `Rename`; preserve mode; hold an exclusive lock (`.git/doc-connector/lock`) for the read-modify-write.
- If an existing pattern already causes our path to be ignored by this file, do not add a duplicate.
- Never rewrite or reorder unrelated lines.

**Warning signs:**
`git check-ignore` matching a pattern the user never wrote; two identical lines after running `init` twice; a team directory suddenly appearing in `git status`.

**Detection test:**
Fixture where `info/exclude` contains `build/` with **no trailing newline**; run `init` twice; assert byte-identical file after the second run, `git check-ignore build/x` still rc 0, and docs ignored.

**Phase to address:**
P2 — Isolation guard.

---

### Pitfall 12: Documents are already tracked, or `git add -A -f` stages them

**What goes wrong:**
Someone (possibly the user, before adopting the tool) committed `private-docs/` once. From then on, ignore rules have zero effect, `git status` shows edits, and `git add -A` keeps committing them into team history. The tool's exclude line is written, the guard that only consults `check-ignore` returns "not ignored" and blocks everything with an unhelpful message; a guard that only checks `status` is clean may miss staged-but-uncommitted docs.

**Why it happens:**
Ignore rules never apply to tracked paths; this is the single most misunderstood part of gitignore. `git add -f` and IDE "add all" buttons also bypass exclusion deliberately.

**How to avoid:**
- Guard on `git ls-files -z -- <docs_dir>/` (must be empty) plus `git status --porcelain -z -- <docs_dir>/` (must be empty), in addition to the ignore-source check.
- On detection, refuse all write operations and print a remediation the *user* must run (`git rm -r --cached private-docs && git commit`), making clear the tool will not modify the index or history.
- Never run `git add`, `git rm --cached`, `git update-index`, or `git reset` in the main repository — not even "to fix it".

**Warning signs:**
`git ls-files` listing anything under the docs directory; a guard message that says "not ignored" on a repo where the pattern is present; teammates reporting private files in the remote.

**Detection test:**
`git add -f private-docs/x.md` (no commit) then run `status`: assert exit 3 with "tracked"; commit it and assert the same; assert the working tree and index are unchanged (compare `git status --porcelain` before/after).

**Phase to address:**
P2 — Isolation guard; also a hard precondition in P3/P4 entry points.

---

### Pitfall 13: `git clean -x` deletes the documents, and `git stash --all` copies them into the team repository

**What goes wrong:**
- `git clean -xfd` removes ignored files, including the entire docs directory (§Verified: `Would remove docs/`, then the directory was gone).
- `git stash push --all` stores ignored files in the stash commit's third parent (§Verified: `docs/s.md` appeared in `stash@{0}^3`), putting private documents into the team repo's object database — a direct violation of "never pollute the main repository", even if the stash is never pushed.
- `git stash push -u` correctly leaves ignored files alone (§Verified: no third parent, file untouched).

**Why it happens:**
Both commands are muscle memory for "reset my working tree". Exclusion is invisible in their mental model.

**How to avoid:**
- Cannot be prevented technically without touching team-shared files. Print the hazard in `init` output, in `status` when conflicts exist, and in `--help`: use `git clean -fd` (no `-x`) and `git stash -u` (not `--all`).
- Provide recovery: deleted docs are recoverable with `pull` from the private repo *if* the last push was recent; `restore` can bring back a single file. State the "unpushed changes are gone" limit explicitly.
- Optional `doctor` check in `status`: if the docs directory is missing while config exists, say "deleted (possibly by `git clean -x`); run pull".

**Warning signs:**
User reports "my notes vanished after `git clean`"; stash list containing a stash whose third parent has a `<project_id>/` tree; `git fsck --unreachable` listing blobs under the docs path.

**Detection test:**
Integration test asserting the warning string is printed by `init` and `status`; a fixture test that runs `git stash push -u` and asserts `stash@{0}^3` does not resolve.

**Phase to address:**
P2 (warnings and guard) and P5 (recovery messaging for a missing docs directory).

---

### Pitfall 14: The commit is built on the wrong parent, or the remote's initial state is assumed

**What goes wrong:**
Four variants:
1. The commit is parented on the *last synced* commit instead of the freshly fetched remote head. Any concurrent push — even from another project sharing the repo — makes the push non-fast-forward (`! [rejected] ... (fetch first)`, §Verified).
2. The remote was created with a README on `main`. Building a commit from an empty tree and pushing is rejected as non-fast-forward; naive code then "fixes" it with `--force`, destroying the README commit.
3. The remote is completely empty, so there is no parent and no ref: `git ls-remote --exit-code <url> <ref>` exits 2 (§Verified), which is misread as an error. Worse, fetching an empty remote with a narrow **non-glob** refspec fails hard — `git fetch <url> +refs/heads/main:refs/heads/main` → `fatal: couldn't find remote ref refs/heads/main`, rc 128 — while the glob form `+refs/heads/*:refs/remotes/origin/*` returns 0 (§Verified).
4. The remote's default branch is not `main`, or the fetched refspec is narrower than the pushed ref, so the fetched head and the pushed ref are different commits.

**Why it happens:**
Push is implemented as "commit locally, then upload", and the remote's head is treated as a constant (whatever was synced last). Git only requires that the *new tip* descend from the *current* remote value, which must therefore be read immediately before building the commit. The empty-remote case additionally has no ref to fetch, and its failure is reported as a refspec error rather than "there is nothing there yet", so it gets conflated with a network problem.

**How to avoid:**
- Always: `ls-remote --exit-code` the target ref *before* fetching (rc 0 = exists, 2 = absent, other = error). If absent, skip the fetch entirely (there is nothing to fetch and a narrow refspec would fail); if present, fetch with a glob refspec filtered by ref name or an explicit refspec that is known to exist.
- Build `tree = (remote_head ? remote_head_tree : empty_tree)` with `<project_id>/` replaced; `parent = remote_head` (omit `-p` for the first push).
- For the very first push you may assert non-existence atomically with `git push --force-with-lease=<ref>:` (empty expected value means "must not exist" per git-push docs), so a concurrent creator cannot be overwritten.
- Never assume a branch name: store the push ref (`refs/heads/<branch>`) in config at `init`, defaulting to a dedicated branch rather than `main` (this also avoids colliding with a user's README or manual edits).
- Use `git ls-remote` output length (40 vs 64 hex) to detect SHA-1 vs SHA-256 remotes before creating the store, and initialize the bare store with the matching `--object-format`. A store created under a global `init.defaultObjectFormat=sha256` cannot fetch a SHA-1 remote.
- If `parent` is not `remote_head`, abort before creating objects.

**Warning signs:**
Push rejected "fetch first" on the very first push; a remote whose only commit is "Initial commit"; `store.git/config` with `remote.origin.fetch` not matching the pushed ref; `ls-remote` SHAs of unexpected length.

**Detection test:**
Three fixtures — empty remote, remote with README on `main`, remote with an extra branch — plus a SHA-256 remote (`git init --object-format=sha256`). Assert each push is a fast-forward and that the README commit remains an ancestor.

**Phase to address:**
P3 — Scan & push (store/ref bootstrap also P1).

---

### Pitfall 15: An unpushed commit is garbage-collected and the baseline becomes a dangling SHA

**What goes wrong:**
`git commit-tree` creates an unreachable commit. If the push fails and the SHA is stored only in `config.yaml`, any `git gc` can delete it: with `--prune=now` it disappears immediately, and with the default `gc.pruneExpire` after two weeks (§Verified: `cat-file -t` failed after `gc --prune=now` with no ref; survived when a ref existed). Reflogs do not save you in a bare repository, where `core.logAllRefUpdates` defaults to false.

**Why it happens:**
Objects look permanent; only refs are. A "pending" commit is conceptually a ref but is stored as a string.

**How to avoid:**
- Immediately after `commit-tree`, `git update-ref refs/doc-connector/<project_id>/pending <sha>`. Push that SHA; on confirmed success, move the baseline ref and delete `pending` in one `update-ref --stdin` transaction.
- Set `gc.auto=0` and `gc.pruneExpire=never` in `store.git/config`; run repacks explicitly and only with a grace period.
- Before using any SHA from config, prove it exists: `git cat-file -e <sha>^{commit}`. On failure, go to the recovery path (Pitfall 16), never to "pretend it is there".

**Warning signs:**
`git fsck --lost-found` in the store; `count-objects -v` shrinking between runs; `cat-file` errors mentioning a SHA the config references; a user running `git gc` in the store after a disk-space warning.

**Detection test:**
Build a commit, do not push, run `git -c gc.pruneExpire=now gc --prune=now`; assert that with the pending ref the object survives and that `status`/`push` can still use it.

**Phase to address:**
P3 — Scan & push (state machine and refs).

---

### Pitfall 16: The store is shallow, or the baseline commit no longer exists upstream

**What goes wrong:**
A store created with `git clone --depth` (or a user "optimizing" it) cannot be pushed to ("shallow update not allowed") and lacks ancestry for `log`. Separately, a history rewrite plus server-side gc on the private repo removes the commit the baseline points at: `commit-tree -p <missing>` fails, `status` cannot compute a diff, and `restore` cannot find the file.

**Why it happens:**
The store is treated as a cache, so people assume it can be shallow and disposable — but it is the source of truth for `log`/`restore` and the parent chain for pushes.

**How to avoid:**
- Assert `git -C store rev-parse --is-shallow-repository` is `false` at init and before every operation; if true, offer a full re-clone of the store (docs are intact).
- On missing baseline: do *not* silently re-base. Report "baseline commit <sha> missing upstream", list what is still recoverable (`git log --all`, backup refs), and require `--rebase-baseline=<sha>` where the user names the remote commit to adopt.
- Keep `refs/doc-connector/backup/<timestamp>/<old-head>` on every unexpected rewind (Pitfall 17) so a missing baseline is often still locally present.

**Warning signs:**
`shallow` file in `store.git`; `git log` in the store showing a single commit; push errors containing "shallow"; `cat-file -e` failing on the config's baseline.

**Detection test:**
Two fixtures: a shallow store (assert refusal and that a full re-clone repairs it) and a remote whose head was rewritten and gc'd (assert baseline failure is reported, no commit is created, and `--rebase-baseline` converges).

**Phase to address:**
P3 — Scan & push, with recovery messaging in P5.

---

### Pitfall 17: A force-push rewinds the store ref and quietly discards history

**What goes wrong:**
A user (or a second machine's stale tool) force-pushes the private repo. Our fetch uses a forcing refspec (`+refs/heads/x:refs/remotes/origin/x`, the default for `git fetch <remote>`), which **silently accepts the rewind** (§Verified: rc 0, no warning). The old commits remain in the store but become unreferenced; the next `log` shows a shorter history, `restore` cannot reach deleted versions, and the next push rebuilds from the new head — reviving a state other machines already deleted, or losing documents entirely.

**Why it happens:**
Forced refspecs are the default for fetch; only an explicit non-forcing refspec reports `! [rejected] ... (non-fast-forward)` (§Verified). People assume fetch cannot lose data.

**How to avoid:**
- Read the current tracking ref before fetching; after fetching, compare: if the old value is no longer an ancestor of the new value, create `refs/doc-connector/backup/<unix-ts>/<old-sha>` before proceeding.
- Decide policy explicitly: warn + continue with backup (default), or abort with `--accept-rewind` required.
- Never use `--force` for our own pushes; if a rebase is genuinely needed (should not be, given Pitfall 14's parent choice), use `--force-with-lease=<ref>:<expected>` with an expected value obtained from an immediately preceding `ls-remote`. Note that `--force-with-lease` alone fails with `(stale info)` if the tracking ref was not refreshed (§Verified).

**Warning signs:**
`git merge-base --is-ancestor <old> <new>` returning false; reflog-less bare store; `log` count decreasing between runs; a teammate saying "I cleaned up the docs repo".

**Detection test:**
Local bare remote fixture; force-push an older commit from a second clone; run `pull`; assert a backup ref exists, the warning names both SHAs, and no local documents were deleted.

**Phase to address:**
P3 (fetch/ref policy) and P4 (pull must not treat a rewound remote as a normal update).

---

### Pitfall 18: Concurrent pushes from two projects collide, and the "fix" clobbers the other project

**What goes wrong:**
The private repo is shared, so project A's push and project B's push race on one branch. The loser receives a non-fast-forward rejection (correct), then retries with `--force` "because it is my private repo" — deleting B's commit. B's documents survive only on B's disk and only until the next gc.

**Why it happens:**
A shared branch looks like a single-writer resource. The docs-only diff makes force-pushing feel harmless, but the ref is shared across projects.

**How to avoid:**
- Bounded retry loop (3 attempts, jittered): fetch → verify the remote `<project_id>` subtree still equals the baseline → rebuild the tree as remote tree with only our prefix replaced → push.
- Assert, before every push, that the tree being pushed is byte-identical to the remote head outside `<project_id>/`; if not, a bug in the tree merge, abort.
- Never expose a `--force` flag in the CLI at all. If an escape hatch is needed, make it `--force-with-lease` only, never `--force`.
- Log the retry decision (`remote advanced by <sha> from another project; rebuilding`) so the behaviour is explainable.

**Warning signs:**
Push retries in consecutive runs; commits by other projects interleaved in `log`; a user asking "why did my other project's docs disappear".

**Detection test:**
Two goroutines pushing different prefixes in a 20-iteration loop against one bare remote; assert all 40 commits are reachable, no rejection escapes the retry loop, and the final tree contains both prefixes.

**Phase to address:**
P3 — Scan & push.

---

### Pitfall 19: The baseline advances even though the push failed, or the push outcome is unknown

**What goes wrong:**
Three failure shapes:
1. Objects uploaded, ref rejected (verified `! [rejected]`), but config already says "synced at <sha>" — `status` reports clean while the remote never got the commit.
2. The connection drops after the server updated the ref. Locally the push "failed"; the remote actually advanced. Next run re-pushes an identical commit (harmless) or, worse, treats the stale baseline as truth and builds a divergent commit.
3. Auth expires mid-transfer: pack uploaded, ref not updated.

**Why it happens:**
The exit code of `git push` conflates "rejected", "unknown", and "transport error". Baseline writing is treated as bookkeeping rather than part of the transaction.

**How to avoid:**
- Commit the baseline only after an authoritative read: `git ls-remote <url> <ref>` equals the pushed SHA, or has it as an ancestor. Do not trust stdout parsing alone.
- Keep a `pending` state in config (`{commit, parent, remote_head, started_at}`), and reconcile on every startup: remote == commit → success; remote descends from commit → success (someone pushed on top); otherwise re-push or discard.
- Write config atomically (temp + rename) and fsync; never partially update.
- Distinguish exit codes: parse `git push --porcelain` lines (`ok` vs `!`) plus stderr for transport errors, and map them to distinct user-facing outcomes.

**Warning signs:**
`status` clean while another machine shows "behind"; repeated identical commits in `log`; push attempts that take minutes and then report success without a remote check.

**Detection test:**
Fake remote wrapper that (a) rejects the ref after receiving the pack, (b) accepts the ref then kills the connection. Assert in both cases that the next run converges and never reports "clean" against a remote that lacks the commit.

**Phase to address:**
P3 — Scan & push (state machine); P5 must display the pending state rather than a false "clean".

---

### Pitfall 20: Large files hit the host's hard limit after a long upload

**What goes wrong:**
A 120 MiB scanned PDF fails the push at the provider (GitHub blocks >100 MiB, warns >50 MiB), usually after the pack has been uploaded. Depending on the host, the error is an opaque HTTP failure; the user retries repeatedly. Without LFS (out of scope), the file can never be synced.

**Why it happens:**
"No LFS" was decided for note-sized content, but nothing enforces the assumption. Sizes are only discovered at push time.

**How to avoid:**
- Preflight during scan, before hashing: stat every file; `>25 MiB` warn with the file list, `>90 MiB` hard error naming the largest offenders and suggesting external storage. Make the limits configurable with documented defaults tuned to the host.
- Enforce the total project budget too (`>500 MiB` warn, `>2 GiB` error), because every edit of a binary file adds a full blob to history; GitHub recommends <1 GiB, <5 GiB strongly (§Sources).
- Report the projected object count/bytes before pushing on the first push, so a slow upload is a decision, not a surprise.

**Warning signs:**
Push durations of minutes; `git count-objects -vH` in the store growing by the file size on each edit; HTTP 413 or "remote: error: File ... exceeds" messages.

**Detection test:**
Sparse 95 MiB file in the docs directory: assert `push` fails before any network call (fake remote asserts zero connections), names the file, and leaves the baseline untouched.

**Phase to address:**
P3 — Scan & push (preflight), configurable in P6.

---

### Pitfall 21: `hash-object --path` applies line-ending conversion and creates permanent churn

**What goes wrong:**
Hashing with `--path=<name>` runs the attribute machinery: with `core.autocrlf=true`, a CRLF working file is stored with LF while the file on disk stays CRLF (§Verified: blob `b77b4eb…` vs raw `0aa0f34…`). Pull then writes LF bytes to a CRLF file; the next scan hashes the disk bytes, sees a difference, and commits again — every push, forever. A `.gitattributes` with `* text=auto` in the private repo makes it worse and machine-dependent.

**Why it happens:**
`--path` is the documented way to "respect attributes", which sounds correct. But this tool's contract is byte-faithful storage, not checkout semantics.

**How to avoid:**
- Always hash with `--no-filters`; never use `--path`; never use `git add`/`checkout-index` anywhere in the pipeline.
- Batch hashing with `--stdin-paths --no-filters` (verified to preserve CRLF and to reject `--path`, so the batch path is inherently unfiltered) and rely on the portable-name rule to guarantee no newline in paths.
- Write pulled bytes verbatim; do not open files in text mode (Go is binary-safe by default, but `\r\n` translation creeps in via helper libraries).
- Optionally ship `* -text` as `.gitattributes` at the private repo root so human clones also behave, but do not depend on it.

**Warning signs:**
`git ls-files --eol` in a normal clone showing `i/lf w/crlf`; every `status` reporting the same file modified after a clean pull; diffs that are whitespace-only.

**Detection test:**
CRLF file → push → pull into a fresh fixtures dir → `push` again: assert zero changes and byte-identical content (`bytes.Equal` against the original).

**Phase to address:**
P3 — Scan & push (and P4 for byte-exact writes).

---

### Pitfall 22: Mode bits and symlinks flip-flop between platforms

**What goes wrong:**
- Windows has no executable bit. Deriving `100755` from `os.Stat` yields `100644` on Windows, so a script pushed from Linux becomes non-executable on the next Windows push; Linux pulls it back as `100755`, and the cycle repeats as a permanent diff.
- `core.fileMode=false` on DrvFs/Windows (§Verified on `/mnt/c`) means the local mode is meaningless.
- Symlinks are `120000` entries. With `core.symlinks=false` (Windows default) a symlink appears as a small plain file containing the target path, so the next push records `100644` and rewrites the link as a text file. Pulling a `120000` on Windows creates a text file rather than a link.

**Why it happens:**
The tool builds trees without an index, so git's usual `core.fileMode` handling does not apply; mode must be modelled explicitly, and most implementations forget it until the first script is synced.

**How to avoid:**
- Mode policy, written down and tested: for existing paths, preserve the mode from the baseline/remote tree; only change `644↔755` when running on a POSIX filesystem *and* `core.fileMode` is true. On Windows, never emit a mode change.
- Treat symlinks as unsupported: `Lstat` every path, skip symlinks with a warning (or sync the target's content if the user opts in) and never emit `120000`. On pull, seeing `120000` in a tree is a named error on Windows and a `os.Symlink` on POSIX with the same skip policy.
- Reject FIFOs, sockets, and device files (`Lstat` mode not regular/dir) — opening them can block forever.

**Warning signs:**
`git diff-tree` showing `:100755 100644` for unchanged content; one file alternating modes in `log`; a docs file whose "content" is a path like `../shared/target.md`.

**Detection test:**
Cross-platform fixture: chmod +x on Linux, push, pull on Windows, push again — assert no mode change in the second commit. Symlink fixture asserts a warning and no `120000` object.

**Phase to address:**
P3 (mode policy), P6 (Windows verification).

---

### Pitfall 23: Plumbing output is parsed as if it were line-oriented and unquoted

**What goes wrong:**
- `git ls-tree` C-quotes non-ASCII and tab-containing paths unless `-z` is used (`"d/\344\270\255…"`), so the scanner creates a file literally named `"d/\344…"`. (§Verified)
- `git diff-tree --name-status -z` emits `status\0path\0` pairs, not `status<TAB>path` lines; a line splitter misaligns every record. Rename detection adds `R100` with two paths, silently converting moves into deletions/additions at the wrong layer. (§Verified with `--no-renames`: `D`, `M`, `A` and NUL pairs.)
- `git for-each-ref --format` applies its own quoting rules for some atoms.
- Pathspec magic (`:(glob)`, `:(exclude)`) applies after `--`; the current `<project_id>/…` shape makes injection structurally impossible, but a future path shape should not have to rely on that.

**Why it happens:**
Each plumbing command has a default "human" output mode that is quoted and line-oriented, with `-z` (and `--no-renames`) as opt-ins documented in a separate paragraph. Parsers are written against the first fixture someone tries — ASCII names, no renames, no tabs — and every one of those hazards is invisible until a real document name or a directory move appears.

**How to avoid:**
- Use `-z` on every command that emits paths (`ls-tree`, `diff-tree`, `ls-files`, `check-ignore`, `status`).
- `--no-renames` everywhere: this design's semantics are per-path, and rename detection is a heuristic that changes the operation set.
- Set `GIT_LITERAL_PATHSPECS=1` for all children, and always separate revisions from paths with `--`.
- Parse NUL records with explicit field counts per command; never `strings.Split(output, "\n")`.

**Warning signs:**
Files with quotes or octal sequences in their names; diffs that report renames you did not perform; parsing bugs that appear only with non-ASCII documents (common for this user base).

**Detection test:**
Fixture with `中文 文件.md`, a tab in a name, and a rename; assert the scanner round-trips all names byte-identically and that a rename appears as delete+add.

**Phase to address:**
P3 — Scan & push (also P5 for `log -- <prefix>`). 

---

### Pitfall 24: `commit-tree` fails because no author identity is configured

**What goes wrong:**
`git commit-tree` aborts with `fatal: empty ident name ... not allowed` and exit 128 when `user.name`/`user.email` are unset (§Verified). This is common in fresh containers, CI, and machines where only the team repo has local identity. Plumbing does not fall back the way the user expects, and the failure surfaces as a generic "git command failed".

**Why it happens:**
Porcelain commands like `git commit` also fail, but people rarely run them without identity; automated tools run in environments where identity was never configured.

**How to avoid:**
- Preflight in the *store* repository with `git var GIT_AUTHOR_IDENT`; on failure, print the two exact `git config` commands and exit with a dedicated code before scanning or hashing.
- Optionally support `--author "Name <email>"` and `doc_connector.author` in config, but never invent an identity silently: the author is the audit trail for `log`.
- Also honour `user.useConfigOnly`: if set and identity is missing, the error message must not suggest a global config write.

**Warning signs:**
CI logs ending at the commit step; commits in `log` authored by `root@localhost`; `git config user.email` empty while `GIT_AUTHOR_EMAIL` is set but empty.

**Detection test:**
Spawn `git` with `HOME` and `XDG_CONFIG_HOME` pointing at empty temp dirs; assert `push` fails before creating any object (`store` object count unchanged) and the error contains `git config user.email`.

**Phase to address:**
P3 — Scan & push (preflight, also used by P1's store self-test).

---

### Pitfall 25: A document name that is legal on Linux is unwritable or dangerous on Windows

**What goes wrong:**
Git on Linux accepts and checks out names that Windows cannot represent: `CON`, `NUL.txt`, `COM1`, names ending in a dot or a space, names containing `< > : " | ? *` or control characters, and paths over `MAX_PATH` (§Verified: `mktree`/`read-tree` accept `CON`, `trail.`, `C:evil.txt`; Microsoft documents the reserved set and the 260-character limit). Pull then fails partway through, leaving a half-applied working tree; push creates documents that no Windows machine can ever receive.

**Why it happens:**
Validation is done against the local filesystem only. `core.protectNTFS`/`core.protectHFS` protect git's own checkout, but this tool writes files itself, so git's protection never runs.

**How to avoid:**
- One `ValidatePortablePath(p string) error` used in *both* directions: on push (reject with a rename suggestion, so the repository never contains unportable names) and on pull (validate the entire remote tree before writing a single byte).
- Rules: no empty/`.`/`..` components; no `\`; no component ending in `.` or space; no reserved device names (case-insensitive, with or without extension, including `COM¹²³`/`LPT¹²³`); no `< > : " | ? *` or bytes `0x00–0x1F`; component ≤255 bytes; full relative path ≤ 200 bytes (conservative, leaves room for the repo path and `MAX_PATH`).
- On pull, if the *local* platform cannot represent a remote path, fail the whole pull with the list — do not skip silently and do not write the subset.

**Warning signs:**
Windows users reporting "cannot create file" mid-pull; a docs folder containing `Thumbs.db`-like device names; names imported from macOS with trailing spaces.

**Detection test:**
Table-driven unit tests over the rules; integration pull fixture containing `CON`, `trail.`, `a:b.md`, and a 300-character path — assert abort with all offenders listed and zero files written.

**Phase to address:**
P3 (push-side rejection) and P4 (pull-side preflight); re-verified in P6 on Windows.

---

### Pitfall 26: A backslash inside a tree entry becomes a path separator on Windows

**What goes wrong:**
`a\b.txt` is a single, legal tree entry on POSIX (verified: `mktree` and `read-tree` accept it). On Windows, joining the docs root with that name yields `docs\a\b.txt`, creating a directory that the tree never described. With a name like `..\..\evil.txt` (again a single entry), the pull writes outside the docs directory and outside the repository. A hostile or merely macOS-authored private repo can therefore cause arbitrary file writes within the user's permissions.

**Why it happens:**
Path joining is delegated to `filepath.Join`, which treats `\` as a separator on Windows. Git's own checkout is protected by `verify_path`, but only for names it writes; this tool writes the files itself and never asks git to validate.

**How to avoid:**
- Reject any path component containing `\` at both boundaries (Pitfall 25 already does this — keep it as a named, tested rule, not a side effect).
- Build targets with `filepath.FromSlash(validatedSlashPath)`; then assert `strings.HasPrefix(target, docsRoot+string(os.PathSeparator))` on the cleaned absolute paths.
- Use `os.Root` (Go 1.24+; `Root.Rename`/`WriteFile`/`MkdirAll` arrive in Go 1.25, so pin `go.mod` at 1.25+) for all writes so `..` and symlinked parents cannot escape, and keep the Go toolchain patched (`os.Root` itself has had traversal CVEs, e.g. CVE-2026-39822).
- Never follow a symlinked parent inside the docs directory: `Lstat` each component or rely on `os.Root`'s traversal resistance.

**Warning signs:**
Unexpected directories appearing next to, not inside, the docs folder; a remote tree entry whose `ls-tree -z` output contains `\`; Windows users reporting "path not found" during pull.

**Detection test:**
Craft a tree with `hash-object -w -t tree --literally` containing entries `a\b.txt` and `a\..\..\evil.txt`; run `pull` into a sandbox; assert it aborts, lists both paths, and nothing exists outside the sandbox (including one level up).

**Phase to address:**
P4 — Pull & conflicts (write path), with the same validator in P3.

---

### Pitfall 27: Case-insensitive and Unicode-normalizing filesystems silently merge two documents

**What goes wrong:**
The remote contains `Notes.md` and `notes.md` (created on Linux), or `café.md` in NFC and NFD. On Windows, macOS/APFS, and WSL's `/mnt/c` (§Verified case-insensitive), both names resolve to one file: the second write replaces the first's content, and the user sees one document where the remote has two. Pull reports success. On the next push, one of them is deleted remotely for everyone.

**Why it happens:**
The local scan sees only the names the filesystem exposes; the remote tree is treated as a set of independent paths. Collision detection requires folding exactly the way the target filesystem folds (case, and Unicode normalization on APFS/HFS+).

**How to avoid:**
- Compute `collisionKey(path) = strings.ToLower(norm.NFC(path))` per component and reject duplicates in the *remote* tree before writing, naming both paths.
- On scan, also fold local paths: if two files collide locally (possible on Linux), refuse to push — the repository must stay portable.
- Treat a local path whose folded form matches a different remote path as a rename, not as an unrelated add/delete (see Pitfall 28 for apply order).
- Normalize to NFC when storing; document that NFD-only distinctions are not representable.

**Warning signs:**
Pull "succeeding" with fewer files than `ls-tree -r | wc -l`; users on macOS reporting missing notes; `git diff-tree` showing a delete for a file that still exists.

**Detection test:**
Fixture with `Notes.md` + `notes.md` and `café.md` in NFC + NFD; pull into `/mnt/c` (case-insensitive) and into `/tmp`; assert both abort with the full collision list, before any write.

**Phase to address:**
P4 — Pull & conflicts (scan-side check in P3).

---

### Pitfall 28: A case-only rename is applied in the wrong order and destroys the new content

**What goes wrong:**
`Notes.md` → `notes.md`. The plan says delete `Notes.md`, add `notes.md`. On a case-insensitive filesystem, writing `notes.md` first overwrites `Notes.md`'s content, then deleting `Notes.md` removes the file that was just written — or the delete fails and the old name persists with new content. The tree and the working copy disagree until the next push.

**Why it happens:**
The operation list is unordered or sorted lexicographically; correctness depends on a filesystem property that the code never models.

**How to avoid:**
- Detect delete/add pairs whose folded keys are equal and emit a single `rename` operation.
- Apply renames in two steps via a unique temporary name in the same directory (`.<name>.dc-tmp-<pid>`), then fsync, then rename to the final name; journal each step so a crash is resumable.
- Re-verify after apply with a folded-name scan before writing the baseline.

**Warning signs:**
A file whose content matches the new name but whose case matches the old one; `git status` in a separate clone showing both a delete and an add; pull needing a second run to converge.

**Detection test:**
Case-rename fixture applied on a case-insensitive mount; assert exactly one file exists, with the new name and content. Run with kill-after-step to verify journal resume.

**Phase to address:**
P4 — Pull & conflicts.

---

### Pitfall 29: Non-atomic writes, antivirus locks, and cross-volume renames leave half a pull on disk

**What goes wrong:**
- Writing in place truncates the old content; a crash or a failed write leaves an empty or partial document — the exact "lost my notes" outcome the project exists to prevent.
- On Windows, Defender/OneDrive can hold a handle: `os.Rename` fails with a sharing violation, and repeated pulls leave `.tmp` files and a stale baseline.
- If `private-docs` is a junction/symlink to another volume, temp files under `.git` (or vice versa) hit `EXDEV` on rename. Network shares may not give rename atomicity at all.

**Why it happens:**
POSIX intuition ("rename is atomic") plus a single-platform development environment (this repo is developed on `/mnt/c`, a 9p DrvFs mount) hides both Windows and cross-device behavior.

**How to avoid:**
- Temp file in the *destination directory* (not a global tmp), `Sync()`, `Rename()`, then fsync the directory on POSIX; never truncate the destination.
- Windows retry policy: 5 attempts, 50–250 ms backoff, on `ERROR_SHARING_VIOLATION`/`ERROR_ACCESS_DENIED`; surface the offending path after the last attempt, leaving the original intact.
- Detect `EXDEV` and fall back to copy-to-temp-in-destination + fsync + rename + remove; record the fallback in the journal.
- Refuse a docs directory that is a symlink/junction (see also Pitfall 13's `git clean` hazard).

**Warning signs:**
Zero-byte documents after a crash; `.dc-tmp-*` files accumulating; pull failures only on Windows or only for files open in Word/Excel; `errno 18` (EXDEV) in logs.

**Detection test:**
Fault-injection test that kills the process between temp-write and rename, then asserts the original file is intact and a rerun converges. On Windows CI, hold a file open and assert the error names it.

**Phase to address:**
P4 — Pull & conflicts; Windows specifics in P6.

---

### Pitfall 30: mtime and clock skew make the scanner miss or invent changes

**What goes wrong:**
A scanner that trusts `mtime` (or a `mtime`-based cache) misses edits on filesystems with 2-second timestamps (FAT/exFAT), after `touch -r`, after clock jumps, or when tools preserve timestamps on save. It can also invent changes when a file's mtime is in the future (clock skew), producing empty commits or spurious conflicts.

**Why it happens:**
`git status` is fast because it uses stat data, so it feels like the right pattern. But git has an index with racy-timestamp handling; this design has none.

**How to avoid:**
- Always hash file content on scan. The expected corpus (hundreds of notes, tens of MB) makes this cheap; `--stdin-paths` batching keeps process count at one.
- If a cache is added later, key it on `(size, mtime_ns, inode)` and invalidate entries whose mtime is in the future or older than the baseline commit's timestamp; treat a cache as an optimization only.
- Never use wall-clock time to order commits or to decide "who is newer"; ancestry decides.

**Warning signs:**
Edits that only appear after `touch`; identical content pushed twice; `status` reporting changes immediately after a clean pull; test suites that pass locally and fail on CI with coarse timestamps.

**Detection test:**
Modify a file, then `touch -r reference file` to restore its old mtime; assert `push` still detects the change. Set the clock forward/backward via `faketime` if available and assert no spurious commit.

**Phase to address:**
P3 — Scan & push (design decision), re-checked in P6.

---

### Pitfall 31: A missing docs directory is indistinguishable from "delete everything"

**What goes wrong:**
The docs directory lives on a removable drive, a OneDrive folder in "free up space" state, or was renamed/moved. The scan finds zero files, the diff against the baseline is "delete all", and `push` removes every document from the remote — then every other machine pulls the deletions. A single `mv` plus one push destroys the corpus everywhere.

**Why it happens:**
The scanner's input is "whatever exists now"; absence is overloaded to mean deletion. There is no distinction between "user deleted file X" and "the directory is not mounted".

**How to avoid:**
- Hard error when the docs directory is missing or not a directory; never auto-create it during `push` (only `init` may create it).
- Deletion budget: if deletions exceed `max(5, 25% of baseline files)`, require `--allow-deletes` and print the list with sizes. If local file count is 0 while the baseline is non-empty, require an explicit `--allow-deletes=all`.
- `status` always prints `+N ~N -N` counts; `push --dry-run` prints the exact deletion list.
- Keep deleted content recoverable: `pull` can restore from history, and a local trash (`.git/doc-connector/trash/<ts>/`) on pull-side deletions (Pitfall 33).

**Warning signs:**
`status` showing a large negative count; docs directory path containing `OneDrive`/`Dropbox`; empty directory that the user believes is populated; a push that completes suspiciously fast.

**Detection test:**
`mv private-docs /tmp/backup` then `push`: assert exit 2, remote ref unchanged. Repeat with 26% of files deleted: assert the flag is required; with 3 of 200 deleted: assert it proceeds.

**Phase to address:**
P3 — Scan & push; `status` reporting in P5.

---

### Pitfall 32: A crashed pull leaves a half-applied tree and an old baseline

**What goes wrong:**
Pull applies 40 of 60 files, then the laptop sleeps or the disk fills. The baseline still points at the previous remote commit, so the next run classifies newly written remote content as *local* modifications: it may push a mixed state, regenerate conflicts for files that were already resolved, or delete files that were half-applied (if the delete step ran first).

**Why it happens:**
Multi-file apply is treated as a loop of independent operations rather than a transaction. The baseline update is an afterthought.

**How to avoid:**
- Two-phase apply: (1) plan the complete operation list and validate every path; (2) execute with a journal file written *before* each operation (`{seq, op, path, tmp, state}`), fsynced.
- On startup, if a journal exists, resume idempotently: operations marked `done` are verified by content hash, `pending` temp files are completed or removed, and no new plan is computed until the journal is drained.
- Update the baseline last, in the same atomic config write that clears the journal.
- Free-space preflight: sum of bytes to write + 10% must be available, else abort before touching anything.

**Warning signs:**
`journal.json` present at startup; `.dc-tmp-*` files; `status` showing modified files the user never edited; conflicts reappearing for resolved paths.

**Detection test:**
Test hook `DOC_CONNECTOR_TEST_KILL_AFTER=<n>` kills after the n-th operation; loop n from 1 to the operation count, rerun pull, assert convergence to the remote tree and a single conflict set.

**Phase to address:**
P4 — Pull & conflicts.

---

### Pitfall 33: Conflict artifacts get synced, accumulate, and overwrite each other

**What goes wrong:**
`<name>.theirs.<host>` is written into the docs directory, which is exactly the directory the scanner walks. The next `push` uploads all artifacts to `<project_id>/` (visible to every machine), and each subsequent conflict on the same path either overwrites the artifact (losing the previous "theirs" content) or grows `.theirs.host.1`, `.theirs.host.2`. `git clean`-style cleanup can also delete the only copy of the other side.

**Why it happens:**
The artifact is stored next to the document for discoverability, but the sync boundary is the directory, not the file set.

**How to avoid:**
- Record every artifact in `.git/doc-connector/conflicts.json` (`path, theirs_path, base_commit, remote_commit, host, at, kind`) and exclude *exactly those paths* from the scanner — not a broad glob that could hide a legitimately named file.
- As a backstop the built-in ignore list includes `*.theirs.*`; document that the suffix is reserved.
- Refuse `push` while `conflicts.json` is non-empty (exit 2) and list the paths; `status` shows them explicitly.
- Never overwrite an existing artifact: if the path exists with different content, write `<name>.theirs.<host>.<base-short>` and record it.
- Sanitize `<host>` to `[a-z0-9-]`, max 32 chars; use the short baseline SHA when a second conflict occurs.

**Warning signs:**
`.theirs.*` files inside the remote `ls-tree`; more artifacts than conflicts; a `conflicts.json` referencing files that no longer exist.

**Detection test:**
Force a conflict, then `push` (assert refusal), then resolve by editing + deleting the artifact (assert success and an empty conflicts file). Run the same conflict twice and assert two distinct artifacts with both "theirs" contents recoverable.

**Phase to address:**
P4 — Pull & conflicts (creation), P3 (scanner exclusion).

---

### Pitfall 34: A file edited between planning and applying is overwritten by the pull

**What goes wrong:**
The user has the document open. Pull plans "write remote version". The editor autosaves before the rename, and the pull replaces the fresh keystrokes with the remote content — silently, because the plan was computed from a stale snapshot.

**Why it happens:**
The plan/apply split (necessary for correctness) creates a TOCTOU window; the local file's state is not re-validated at write time.

**How to avoid:**
- Capture `(size, mtime_ns, mode, sha256)` per file at plan time.
- Immediately before the rename for that path, `Lstat` + re-hash; if anything differs, abort the apply, roll back to the pre-pull state via the journal, and report "file changed during pull: <path>".
- Hold an advisory lock for the whole operation (`.git/doc-connector/lock`, `O_EXCL` with pid/host and stale-lock detection) so two tool instances cannot interleave, but never pretend it locks editors.
- Offer `--keep-local` for files the user knows they are editing (turns them into conflicts).

**Warning signs:**
"my last sentence disappeared" reports that are not reproducible; file mtimes newer than the pull's start time but older than its end; conflicts appearing right after a successful pull.

**Detection test:**
Test hook that sleeps between plan and apply while the test rewrites a file; assert the apply aborts, the edited bytes survive, and the journal is drained on the next run.

**Phase to address:**
P4 — Pull & conflicts.

---

### Pitfall 35: First pull onto a machine that already has documents destroys or duplicates them

**What goes wrong:**
New machine, fresh `init`, but the user already copied `private-docs/` from a USB stick (or from the old workflow). There is no baseline, so a naive pull treats "local file exists, remote file exists" as an overwrite: local edits vanish. A naive push instead overwrites the remote with stale USB content. If the pull writes conflicts for every file, the user faces 200 `.theirs` files and gives up.

**Why it happens:**
"First sync" is the one state where all three sides are unknown: base is empty, local is populated, remote is populated. Most sync code only handles the steady state.

**How to avoid:**
- Define the first-pull algorithm explicitly: `base = empty`; for each path: local absent → write remote; both present and hash equal → adopt silently; both present and differ → keep local, write `.theirs`, record conflict; local only → leave in place and report "not yet pushed"; remote only → write.
- Never delete on first pull. Never push implicitly from pull.
- Print a summary (`adopted N identical, M conflicts, K local-only pending push`) and exit 2 if any conflict.
- On first push after such a pull, the same three-way rules apply, so local-only files are added and unresolved conflicts block the push.

**Warning signs:**
A first pull taking a long time and producing many artifacts; users reporting "it asked me about every file"; remote prefix suddenly containing old content after a USB copy.

**Detection test:**
Fixture: remote has 10 files; local has 4 identical, 3 divergent, 2 local-only, 1 remote-only. Assert the exact classification, exit 2, local bytes preserved, 3 artifacts with remote bytes, and no deletions.

**Phase to address:**
P4 — Pull & conflicts (first-run path), with `status` explaining the state in P5.

---

### Pitfall 36: Push overwrites remote changes in the same prefix, or misrepresents delete-vs-edit

**What goes wrong:**
- Another machine (or a GitHub web edit) modified `<project_id>/file.md`. Our push rebuilds `<project_id>/` from local files, silently reverting it. The three-way logic only ran on pull; push assumed "remote == baseline".
- Edit-vs-delete: local edited `a.md`, remote deleted it. A per-path merge that writes `.theirs` needs content for "theirs", but there is none; writing an empty file makes the user think the document was emptied, and deleting locally loses the edit.

**Why it happens:**
Push is often implemented as "build tree from disk, commit, push". The remote side of the three-way is only checked at pull time.

**How to avoid:**
- Before building the commit, fetch and compare `remote_head:<project_id>` with `baseline:<project_id>`; if different, abort with "remote has changes under <project_id>; run pull first". This is a single tree comparison (`rev-parse <head>:<pid>`), cheap and decisive.
- Keep the other prefixes byte-identical to the remote head; assert it before pushing (Pitfall 18).
- Edit-vs-delete becomes a conflict kind `remote-deleted`: keep the local file, write an artifact only if remote content is recoverable (baseline blob exists), otherwise record `theirs_absent: true` and let `status` explain that `restore`/history still has the last remote version.
- Delete-vs-edit (local deleted, remote edited): keep the remote version in a conflict artifact and do not delete locally until the user confirms.

**Warning signs:**
Remote edits disappearing after a push from another machine; artifacts with zero bytes; a conflict on a file that does not exist locally.

**Detection test:**
Two clones against one bare remote: clone B commits an edit to `<pid>/a.md`; clone A (stale baseline) pushes; assert abort with the pull-first message and that B's content remains. Separately assert the `remote-deleted` conflict is recorded without an empty artifact.

**Phase to address:**
P3 (pre-push remote check) and P4 (conflict kinds).

---

### Pitfall 37: The baseline is defined as "the last commit we made" instead of "the last agreed content"

**What goes wrong:**
After a `pull`, no local commit is created, so a baseline updated to "current HEAD" is meaningless; if it is not updated at all, every subsequent push tries to re-apply remote changes as local ones, producing a commit that reverts the remote. Conversely, storing the baseline after a push that was later rejected (Pitfall 19) makes the three-way base fictitious, and the next pull sees spurious "local modifications" for every file it wrote.

**Why it happens:**
"Baseline = last synced commit" is ambiguous between "last commit I created" and "last content both sides agreed on". The correct value differs per operation and per partial-conflict outcome.

**How to avoid:**
- State the invariant in code and docs: **baseline is the commit whose `<project_id>` subtree equals the content both sides last agreed on.**
- After a successful push: baseline = pushed commit.
- After a pull: baseline = the pulled remote head, even if local conflict artifacts exist (they are outside the agreed content set and are tracked in `conflicts.json`, which blocks pushes).
- After a first pull with local-only files: baseline = remote head; local-only files are "ours" and will be added on push.
- Persist `{baseline_commit, baseline_tree, pending_commit, conflicts}` in one atomically-written config; recompute `baseline_tree = rev-parse <baseline_commit>:<pid>` at startup and fail if missing.

**Warning signs:**
`status` clean right after a pull that reported conflicts; push reverting remote content with no local edits; `ahead` counts that never reach zero; `baseline_tree` disagreeing with the commit's tree.

**Detection test:**
State-machine test over sequences: push→pull→status (clean), push→edit→pull→status, pull→edit→push→pull, pull with conflict→push (blocked). Assert baseline equals the expected SHA at every step.

**Phase to address:**
P3 and P4 jointly (shared state machine), surfaced in P5.

---

### Pitfall 38: The private repository is untrusted input: resource exhaustion and gitlinks

**What goes wrong:**
The private repo is writable from any machine, and its content can be hostile (compromised account, a malicious collaborator, or just an accidental `npm install` dumped into docs). A tree with 500k entries, a 2 GiB blob, deeply nested directories, or a `160000` gitlink makes pull consume disk/memory/time before failing — or writes a submodule entry that the tool then renders as an empty directory.

**Why it happens:**
"my private repo" is treated as trusted; path validation is remembered, but size/count/type limits are not.

**How to avoid:**
- Preflight the tree with `git ls-tree -r -z -l <commit>:<pid>`: reject gitlinks (`160000`) and non-blob modes, cap entry count (e.g. 20k), depth (e.g. 32), single blob size (e.g. 90 MiB), and total bytes (e.g. 2 GiB) — all configurable, all checked before any write.
- Stream blobs via `cat-file --batch` with a size check; never `git show` a whole object into memory blind.
- Abort with counts (`tree has 412k entries; limit 20k`) rather than attempting a partial pull.
- Apply the same limits to push-side scanning so one machine cannot create an unreasonable tree.

**Warning signs:**
Pull taking minutes before any file appears; `out of memory` in the Go process; an empty directory that the remote reports as a submodule; `du` of the store jumping by gigabytes.

**Detection test:**
Fixtures: 100MB blob, gitlink entry, 40-deep nesting, 25k entries. Assert each aborts with a specific limit message, zero files written, and the baseline unchanged.

**Phase to address:**
P4 — Pull & conflicts (limits also used in P3).

---

### Pitfall 39: A token embedded in `--remote` is persisted and printed

**What goes wrong:**
`init --remote https://user:ghp_xxx@github.com/me/docs.git` writes the URL into `.git/doc-connector/config.yaml` **and** into `store.git/config` (`remote.origin.url`). It then shows up in `git remote -v` output, in error messages, and in any bug report the user pastes. The token also lands in shell history and process arguments.

**Why it happens:**
HTTPS URLs with embedded tokens are the most common quick way to authenticate in scripts; nothing in the UX says "credentials are handled by git, not by us".

**How to avoid:**
- Parse the URL and reject userinfo entirely (`url.User != nil`), with a message pointing at `git credential` helpers and SSH remotes.
- If a URL must be stored, store only the sanitized form and redact in all output (`https://***@host/path`).
- Create config dir/file with `0700`/`0600`; document that the store config also contains the URL.
- Never pass the URL as a command-line argument to git when it can be read from the store config (argv is world-readable on some systems).

**Warning signs:**
`store.git/config` containing `https://…@…`; tokens in bug reports; users rotating tokens after every `init`.

**Detection test:**
`init --remote https://u:token@example.com/x.git`: assert exit 2, no config/store created, and that the token never appears in stdout/stderr.

**Phase to address:**
P1 — Identity & init (validation), re-checked in P6 (error formatting).

---

### Pitfall 40: Git hangs forever on credentials, host keys, or a proxy

**What goes wrong:**
In a non-interactive context (CI, IDE task runner, GUI git client, `nohup`), `git fetch` blocks on a username prompt, on an SSH host-key confirmation, or on a passphrase — indefinitely. The user sees a frozen tool with no output. Proxy failures (`407`, `502`, DNS) are similarly opaque, and retries turn a 2-second failure into minutes.

**Why it happens:**
Git's defaults assume a TTY. `GIT_TERMINAL_PROMPT` and `core.askPass` are environment- and config-dependent, and SSH's `BatchMode` is off by default.

**How to avoid:**
- Every child process gets: `GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS` set to a helper that exits 1 (or an empty `core.askPass` via `-c`), `GIT_SSH_COMMAND="ssh -oBatchMode=yes -oConnectTimeout=15"`, `-c credential.interactive=false`, and `GCM_INTERACTIVE=never` for Git Credential Manager.
- Wrap with a context timeout (connect ~30 s, transfer scaled to size, hard cap) and kill the whole process group so `ssh` dies with git.
- Classify failures from stderr: auth (`Authentication failed`, `Permission denied (publickey)`), host key (`Host key verification failed`), proxy (`CONNECT tunnel failed`, HTTP 407), DNS, TLS, and network reset. Retry only the last two, at most twice, with jitter.
- When auth fails in a TTY, print a one-line "run `ssh -T git@host`" hint rather than re-running git interactively.

**Warning signs:**
Runs that never terminate; CI jobs hitting the wall-clock limit at the fetch step; "terminal prompts disabled" errors that the user cannot map to a cause; a tool that works in the terminal but hangs from the IDE.

**Detection test:**
Remote URL pointing at a blackholed IP and at a host requiring auth; assert both fail within the configured timeout with distinct messages and a non-zero exit, and that no orphan `ssh`/`git` processes remain (`pgrep`).

**Phase to address:**
P6 — Cross-platform & network hardening (child-process policy needed as early as P1's clone).

---

### Pitfall 41: `safe.directory` blocks every git call after an ownership change

**What goes wrong:**
Running as root in a container, via `sudo`, or on a WSL mount where uid mapping changed makes git refuse to even parse the repository config: `fatal: detected dubious ownership in repository at '…'`. Every command in the tool fails, including `rev-parse`, with a message users interpret as "corrupted repository".

**Why it happens:**
Since the CVE-2022-24765 fix, git refuses repositories owned by another user. Adding `safe.directory` to the global config is the usual workaround, which the tool must not do silently (it is a global security setting).

**How to avoid:**
- Detect the exact stderr substring and emit: "repository is owned by uid X but you are uid Y; either fix ownership (`chown -R`) or, if you trust it, run `git config --global --add safe.directory <path>` yourself".
- Never call `git config --global` or set `GIT_CONFIG_GLOBAL` to a file the tool writes without explicit opt-in.
- The same applies to the store: if the store is on a shared/root-owned mount, fail at `init` rather than at the first push.

**Warning signs:**
Every command failing at `rev-parse`; the tool working as the normal user but not under `sudo`/Docker; a repository copied from another machine with `rsync -a` preserving ownership.

**Detection test:**
Container test with the fixture repo `chown`ed to another uid; assert the message names both uids and suggests the exact command, with exit code identical to other preflight failures.

**Phase to address:**
P1 (initial repo open) and P6 (error taxonomy).

---

### Pitfall 42: Nothing is encrypted, and secrets land in history permanently

**What goes wrong:**
A pasted API key, a `.env` copy, or a signed contract PDF is pushed to the private repo. Deleting the file later does not remove the blob from history: it stays reachable from the old commit, survives `git gc` because it is referenced, and is exposed to anyone with repository read access — including a compromised account or a future collaborator. GitHub push protection for repositories requires Secret Protection and is off by default; user-level push protection covers only public repositories (§Sources).

**Why it happens:**
"No encryption; the private repo ACL is the boundary" is a sound decision, but it silently transfers all responsibility to the user. The tool's UX ("sync my docs") implies safety it does not provide.

**How to avoid:**
- `init` prints a short, explicit boundary statement: plaintext, provider-side access control, history is permanent, removal requires a rewrite + force-push + cache purge.
- Optional pre-push scan (on by default, `--no-secret-scan` to disable) for high-signal patterns: PEM private-key headers, `AKIA[0-9A-Z]{16}`, `ghp_`/`github_pat_`, `xox[baprs]-`, `-----BEGIN OPENSSH PRIVATE KEY-----`, and a Shannon-entropy check on suspicious long strings. Report path + line, not the secret itself.
- Handle a provider-side push-protection rejection explicitly: surface the server message, explain that the block is at the host, and that the fix is to remove the secret from the commit (rewrite) rather than to retry.
- Recommend (not enforce) repository-level secret scanning in the private repo settings.

**Warning signs:**
Users pasting keys into notes "temporarily"; push rejected with `push protection` in stderr; a request to add `--force` to "get rid of the blocked commit".

**Detection test:**
Fixture with a fake `AKIA…` line: assert push is blocked with the path/line and no network call; with `--no-secret-scan`, assert it proceeds. Separate test: provider rejection text is surfaced verbatim.

**Phase to address:**
P1 (boundary message) and P3 (scan, rejection handling); P6 for provider-specific messages.

---

### Pitfall 43: Tool-controlled inputs (`--project-id`, `--docs-dir`, config) are used as paths and ref names

**What goes wrong:**
`--project-id ../../x` writes objects and refs at `refs/doc-connector/../../x/pending` (invalid or surprising), `--docs-dir /etc` makes the tool scan and later write outside the repository, and a hand-edited config with `docs_dir: ../shared` escapes the project. A project id containing `/` or `..` also breaks the private repo layout and the `project_id`-prefix guarantee.

**Why it happens:**
These values feel like trusted internal identifiers, but they are user input and the config file is user-editable (and may be synced/copied across machines by the user).

**How to avoid:**
- `project_id` must match `^[0-9a-f]{12}$` unless a documented extended format is introduced; reject anything else.
- `docs_dir` must be relative, slash-normalized, non-empty, not `.`/`..`, contain no `..` component, not equal `.git` or start with `.git/`, and its resolved absolute path must be inside the worktree root (compare with `filepath.Rel`, reject leading `..`). Re-validate on every load, not just at `init`.
- Ref names are derived only from validated ids (`refs/doc-connector/<id>/…`), and `git check-ref-format` is run before writing.
- Reject unknown config fields and version the schema so a hand-edit fails loudly instead of silently disabling a guard.

**Warning signs:**
Config paths containing `..` or absolute paths; `git for-each-ref` showing odd refs; documents appearing outside the project root.

**Detection test:**
Table test over `--project-id` (`../../x`, empty, 13 hex, uppercase) and `--docs-dir` (`../x`, `/etc`, `.git`, `a/../../b`, `private-docs/`); assert all reject with exit 2 and nothing is written.

**Phase to address:**
P1 — Identity & init (validation), re-validated at every config load in P2–P5.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Create the store with `git clone --mirror` | One command, remote + refs set up | Sets `+refs/*:refs/*` and `remote.origin.mirror=true`; fetching pulls every ref (including `refs/pull/*` on some hosts), and a stray `git push --mirror` deletes remote branches | Never. Use `git init --bare` plus one explicit narrow fetch refspec. |
| Store the baseline as a bare SHA in config with no ref | Simple state | `git gc` prunes it; `status`/`push` then fail with "bad object" (§Verified) | Never. Always park pending commits under a ref. |
| Use `git worktree`/`git checkout-index` in the store for pull | Reuses git's merge/checkout and path protection | Creates a second worktree that drifts from the docs directory, applies smudge filters (eol churn), and lets git's path protection differ per platform | Never for this design; the tool owns the working tree. |
| Use `git merge-tree --write-tree` for the three-way | Built-in 3-way merge | Unavailable before git 2.38 (docs version list starts at 2.38.0) while the floor is 2.30, and it performs *text* merges, which the design explicitly forbids | Never with a 2.30 floor; implement path-level three-way. |
| `git add -A` into a temporary index to compute the tree | ~20 lines instead of a tree builder | Inherits `.gitignore` semantics (docs may vanish from the commit), writes an index file, and applies clean filters/autocrlf (§Verified) | Only if you accept "gitignore is the source of truth" — which contradicts the built-in junk list. |
| mtime-based scan cache | Faster on huge trees | Misses edits on coarse/remote filesystems and after clock skew; produces silent data loss | Only as a cache with `(size, mtime, inode)` and a forced rehash when mtime is implausible — and only after hashing is proven correct. |
| Suppress the `*.theirs.*` glob in the scanner as the only artifact mechanism | One-line ignore | Hides a legitimate user file with that name and cannot distinguish open conflicts | Only as a backstop; the authoritative list is `conflicts.json`. |
| Auto-add `safe.directory` to the global git config | Users stop hitting ownership errors | Silently weakens a security check for every repository on the machine | Never automatically; print the command and let the user decide. |
| Retry any failed git command 3× | "Robustness" | Repeats non-idempotent operations, hides auth/proxy errors, and can double-push | Only for transport-level, idempotent fetch/push failures with a fixed, small budget. |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| `git check-ignore` | Treat `rc=1` as fatal, or use it on tracked files to decide ignore status | Use `--no-index -v -n -z`; 0 = matched, 1 = not matched, 128 = fatal; detect tracked docs with `git ls-files -z` (§Verified) |
| `.gitignore` vs `.git/info/exclude` | Assume info/exclude wins because it is "more local" | Precedence is command line > `.gitignore` > `info/exclude` > `core.excludesFile`; require the deciding source to be your exclude file (§Verified) |
| `git ls-tree` / `diff-tree` | Split on newlines and tabs | `-z` everywhere; `--name-status -z` yields `status\0path\0`; add `--no-renames` (§Verified) |
| `git hash-object` | `--path=<name>` "to respect attributes" | `--no-filters`; batch with `--stdin-paths --no-filters` (cannot be combined with `--path`) (§Verified) |
| `git commit-tree` | Assume an identity exists | Preflight `git var GIT_AUTHOR_IDENT`; fail with the two `git config` commands (§Verified rc 128) |
| `git push` | Parse stdout text or only use the exit code | `--porcelain` gives `ok` / `! <ref> [rejected] (reason)`; transport and hook errors are on stderr; verify with `ls-remote` afterwards (§Verified) |
| `git push --force-with-lease` | Expect it to work after a rejection | It fails with `(stale info)` unless the tracking ref was refreshed; fetch immediately before (§Verified) |
| `git fetch` | Use the default forcing refspec and assume it cannot lose commits | A `+` refspec silently rewinds; a non-forcing refspec reports `! [rejected] … (non-fast-forward)`; back up the old value (§Verified) |
| `git rev-parse --path-format` | Assume it works on the 2.30 floor | Added in git 2.31 (absent from the 2.30 docs, §Verified); absolutize paths yourself or bump the floor |
| `git fetch` on an empty remote | Pass an explicit non-glob refspec | Fails rc 128 (`couldn't find remote ref`); use a glob refspec, or skip the fetch after `ls-remote --exit-code` returns 2 (§Verified) |
| `git ls-remote --exit-code` | Treat rc 2 as a failure to report | rc 0 = ref exists, 2 = no matching ref (normal for an empty remote), other = error (§Verified) |
| Object formats | Create the store with whatever `init.defaultObjectFormat` says | Probe `ls-remote` output length (40 vs 64 hex) and create the store with a matching `--object-format` |
| `core.ignorecase` | Assume exclude matching is same everywhere | On case-insensitive configs, `/private-docs/` also matches `Private-Docs/`; Linux does not (§Verified) |
| WSL `/mnt/c` (DrvFs) | Treat it as Linux for tests | 9p mount: modes `0777`, `core.ignorecase=true`, `core.filemode=false`, case-insensitive (§Verified); it is a decent Windows proxy, not a POSIX one |
| GitHub limits | Let the server reject oversized files | Files >50 MiB warn and >100 MiB are blocked server-side (§Sources); preflight locally |
| `git clean -x` / `git stash --all` | Assume ignored files are untouched | `clean -x` deletes them; `stash --all` stores them as objects in the main repo; `stash -u` does not (§Verified) |
| `git worktree` paths | Use `--absolute-git-dir` for config | Use `--git-common-dir` for config/store; `--git-path info/exclude` already points at the common exclude (§Verified) |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| One `git hash-object` process per file | Push takes 60–120 s for a few thousand notes on Windows (process spawn ~10–30 ms each) | Batch `--stdin-paths --no-filters`; keep a single process per push | ~500 files |
| Re-hashing blobs already in the store | Every push costs full-corpus I/O even with no changes | Check existence with `cat-file -e`/`--batch-check` before writing; content-addressed hashing makes this free | ~100 MB of documents |
| Fetching the whole private repo on every `status` | `status` takes seconds and fails offline; shared repo grows with every project | Fetch only the target ref and only for commands that need the remote; cache the last fetched head; offer `--offline` | 3+ projects or ~1 GB repo |
| Binary documents edited repeatedly | Store grows by a full blob per edit; clone/fetch slows for all projects | Warn on >10 MiB binaries, show store size in `status`, document "no LFS"; repack deliberately | A few dozen revisions of a 50 MiB PDF |
| Reading blobs to compare trees | Pull planning reads hundreds of MB to discover nothing changed | Compare OIDs from `ls-tree`/`diff-tree`; read only files that are actually written | ~200 MB corpus |
| `git fsck`/`gc` on every run | Multi-second startup, disk churn | Run `fsck --connectivity-only` on demand; `gc.auto=0` in the store, explicit maintenance | ~10k objects |
| Unbounded conflict artifacts | Docs directory and remote prefix grow without bound | Record artifacts, block push while open, reuse short SHAs in names | Every unresolved conflict |
| `log` without a path limit | `log` walks every project's history, output unusable | `git log -- <project_id>/` and cap with `-n`/`--since` | 5+ projects |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Joining a remote tree path with `filepath.Join` without per-component validation | Backslash/`..` entries write outside the docs directory on Windows (§Verified that git accepts such names on Linux) | Portable-path validator + `os.Root` containment + prefix assertion on the clean absolute path |
| Trusting `core.protectNTFS`/`core.protectHFS` to validate remote names | Those only protect git's *own* checkout; this tool writes files itself | Own allowlist, applied to the whole remote tree before any write |
| Treating the private repo as trusted | Hostile/compromised repo can exhaust disk or write outside the tree | Entry/byte/depth caps, reject gitlinks and non-blob modes, stream with size checks |
| Storing an HTTPS URL with an embedded token | Token in `config.yaml`, `store.git/config`, argv, and bug reports | Reject userinfo; require credential helper/SSH; `0600` perms; redact all output |
| Auto-adding `safe.directory` | Silently disables a security check globally | Detect the error and print the command for the user to run |
| Writing files in place / following symlinked parents | Partial documents after a crash; traversal via a symlinked directory | Temp-in-destination + rename, `O_NOFOLLOW`/`os.Root`, reject symlinked docs dir |
| Relying on the provider to catch secrets | Private-repo push protection requires Secret Protection and is off by default; user-level protection covers only public repos (§Sources) | Explicit plaintext warning at `init`, optional local pattern scan, clear rejection handling |
| Printing remote URLs unredacted in errors | Credentials leak into logs/CI output | Central redaction helper used by every error path |
| Passing user paths to `git` without `--` and literal pathspecs | Pathspec magic or revision/path ambiguity operates on the wrong object | `--`, `GIT_LITERAL_PATHSPECS=1`, explicit revision arguments |
| `restore <path>` accepting absolute/`..` paths | Reads or writes outside the project tree; leaks blobs by SHA | Validate against the project prefix and the baseline tree; read by blob SHA from `ls-tree` |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Conflict exit code 0, or conflicts only mentioned inside a verbose log | User believes sync succeeded, then loses track of which copy is which | Exit 2 on any conflict; print a table `path | local kept | theirs copy` and the exact next step |
| `status` requiring network without saying so | Feels hung offline; fails in a tunnel | Print "fetched in 0.4 s" or `--offline` clearly; still allow exact local-vs-baseline status without network |
| Ahead/behind counts with no file list | "behind by 12" is not actionable | Always list `+A ~M -D` paths, and mark conflicts separately |
| Silently skipping symlinks, junk files, or oversized files | Documents silently never sync; discovered months later | Print skip reasons at the end of every push, with counts; make junk list visible in `--help` |
| Auto-creating an empty docs directory on `push` | Masks an unmounted drive and can lead to mass deletion | Only `init` creates it; `push` errors if missing |
| Reusing `<name>.theirs.<host>` for repeated conflicts | Second conflict silently overwrites the first "theirs" content | Include the short baseline SHA in the name; never overwrite an existing artifact |
| Not printing `project_id` on success | Cannot compare machines; identity drift is invisible | Print `project_id`, root SHA (short), docs path, remote URL (redacted), and store path on `init` and in `status --verbose` |
| Deleting local files on pull without a trace | Directly contradicts the project's stated top fear | Move replaced/deleted local files to `.git/doc-connector/trash/<ts>/` (local only), list what moved, retain for N days |
| Generic "git command failed: exit status 128" | Users cannot self-serve; every failure becomes a support ticket | Map the ~10 known stderr signatures (shallow, dubious ownership, non-fast-forward, auth, proxy, ident, push protection, 100 MiB, LFS, host key) to actionable messages with a stable exit code |

## "Looks Done But Isn't" Checklist

- [ ] **Identity:** `init` refuses shallow clones, archives, bare repos, ambiguous multi-root histories, and non-default-branch roots — verify with the five-fixture table, not just a happy-path clone.
- [ ] **Isolation:** guard requires the deciding exclude source to be `.git/info/exclude` — verify with a `.gitignore` containing `!private-docs/` and with `core.ignorecase=true`.
- [ ] **Isolation:** no git command in the pipeline writes to the index or refs of the main repo — verify by hashing `.git/index` and `git for-each-ref` before/after every command in the integration suite.
- [ ] **Exclude file:** running `init` twice produces a byte-identical `.git/info/exclude` — verify with a pre-seeded file lacking a trailing newline.
- [ ] **Push:** the commit is parented on the freshly fetched remote head and the tree is the remote tree with only `<project_id>/` replaced — verify with a README-initialised remote and with a concurrent second project.
- [ ] **Push:** baseline only advances after `ls-remote` confirms the SHA — verify by rejecting the ref after the pack is received.
- [ ] **GC safety:** an unpushed commit survives `git gc --prune=now` — verify the pending ref exists.
- [ ] **Pull:** a kill at every operation index converges on rerun — verify with the kill-after-N hook across the full operation count.
- [ ] **Pull:** a remote tree containing `CON`, `a\b.txt`, `..`, a gitlink, `Notes.md`+`notes.md`, and NFC/NFD twins aborts before writing anything — verify zero files changed in the sandbox.
- [ ] **Pull:** local edits made during the apply window are not overwritten — verify with the sleep-and-edit hook.
- [ ] **Conflicts:** push is blocked while `conflicts.json` is non-empty, and artifacts never appear in the remote tree — verify both.
- [ ] **Deletion safety:** a missing docs directory aborts the push; >25% deletions require a flag — verify both thresholds.
- [ ] **Modes:** a CRLF document and a `+x` script round-trip between Linux and Windows without mode or EOL churn — verify with two pushes and `diff-tree`.
- [ ] **Identity of commits:** push fails before creating objects when `user.email` is unset — verify store object count is unchanged.
- [ ] **Non-interactive:** every git child fails within the timeout instead of prompting — verify with a blackholed remote and no TTY.
- [ ] **Recovery:** `restore` works when the baseline was force-pushed away (backup ref) and when only the object exists in the store — verify both.
- [ ] **Store integrity:** `init` detects an object-format mismatch before fetching — verify with a SHA-256 remote.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Docs deleted by `git clean -x` | LOW if pushed recently, HIGH otherwise | `pull` from the private repo restores the last synced state; `restore <path> --commit <sha>` for older versions; unsynced edits are unrecoverable — say so |
| Docs accidentally committed to the team repo | HIGH | Stop pushing; the team must rewrite history or accept exposure; rotate any secrets first; the tool must never attempt this automatically |
| Private repo force-pushed and history lost | MEDIUM | Restore from `refs/doc-connector/backup/<ts>` in any store that still has it; otherwise adopt the new head with `--rebase-baseline=<sha>` and accept the gap; re-push local docs |
| Baseline commit pruned/missing | LOW | `git ls-remote` the remote head; if its `<project_id>` subtree matches the last known content, adopt it as baseline; else require `--rebase-baseline` and run pull |
| `project_id` drifted (squash/re-init) | MEDIUM | Re-run `init --project-id <old-id>` to keep the existing prefix; optionally `pull` to re-adopt documents; the orphaned prefix remains in history and needs a manual cleanup decision |
| Pull crashed mid-apply | LOW | Journal auto-resume on next run; if the journal is corrupt, `git -C store` still holds the target tree — delete temps and re-run `pull` |
| Two worktrees with divergent docs | MEDIUM | Pick the authoritative worktree, push from it, remove the docs directory from the other (after copying anything unique), then `pull` there |
| Conflict artifacts accumulated | LOW | `status --conflicts` lists them with base/remote SHAs; resolve, delete artifact, push; the trash directory retains replaced files |
| Store corrupted (`fsck` errors) | LOW | Delete `store.git` and re-initialize; documents and remote are the sources of truth; baseline is recoverable from config |
| Windows lock prevents apply | LOW | Close the file (Word/Excel/OneDrive), rerun; the journal resumes; nothing was truncated |
| Secret pushed to the private repo | HIGH | Rotate the credential first, then rewrite history (`git filter-repo`), force-push with an explicit lease, and purge provider caches; all other machines must re-clone the store |

## Pitfall-to-Phase Mapping

| Phase | Pitfalls owned | Verification gate |
|-------|----------------|-------------------|
| P1 — Identity & init | 1, 2, 3, 4, 5, 6, 7, 39, 41, 43 | Five-fixture repository-resolution table; shallow/multi-root/orphan-branch refusals; token-URL rejection; config written under `--git-common-dir` |
| P2 — Isolation guard | 7, 8, 9, 10, 11, 12, 13, 41, 43 | Negation fixture; tracked-docs fixture; no-newline exclude fixture; double-`init` byte-identity; main repo `.git/index` and refs unchanged across the suite |
| P3 — Scan & push | 1, 6, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 30, 31, 36, 37, 42 | README remote + empty remote + concurrent projects; gc survival; size preflight; CRLF and mode round-trip; remote-prefix-changed abort; blocked-by-conflict abort |
| P4 — Pull & conflicts | 17, 25, 26, 27, 28, 29, 32, 33, 34, 35, 36, 37, 38 | Malicious-tree sandbox abort; case/NFC collision abort; kill-after-N convergence; first-pull classification matrix; journal resume; trash retention |
| P5 — Inspect & recover | 5, 13, 16, 19, 31, 33, 35, 37, 43 | `status` never reports clean against a mismatched remote or open conflicts; `log` path-scoped; `restore` by blob SHA; exit codes documented and tested |
| P6 — Cross-platform & network | 20, 22, 25, 29, 30, 39, 40, 41, 42 | Windows CI: long paths, reserved names, locks, no mode churn; macOS: NFD fixtures; no-TTY run fails fast; provider error taxonomy |

## Verified Mechanics (git 2.47.3, Linux/WSL2 6.18, 2026-10-22)

| Command / scenario | Observed result |
|--------------------|-----------------|
| `git check-ignore` on a tracked file | no output, rc **1**; with `--no-index`: path printed, rc **0** |
| `git check-ignore` on a nonexistent path | rc **0** (pattern matched) |
| `git check-ignore -v --no-index` with only `*` + `!*/` in `.gitignore` | source reported as `.gitignore:2:!*/`, not `info/exclude` |
| `.gitignore` containing `!private-docs/` with `/private-docs/` in `info/exclude` | `git status --porcelain` → `?? private-docs/` |
| Unanchored `private-docs/` vs anchored `/private-docs/` | matches `sub/private-docs/x.md` vs does not |
| `core.ignorecase=true` + `/private-docs/` vs `Private-Docs/` | matched (rc 0); `core.ignorecase=false` → rc 1 |
| CRLF-terminated pattern in `info/exclude` | still matches; pattern printed without `\r` |
| `git rev-list --max-parents=0 HEAD` on a `--depth 1` clone | returns the tip (`2079a5c…`), not the true root (`bdaab60…`) |
| `git rev-list --max-parents=0 HEAD` with two roots | two SHAs; order changed with `--reverse --date-order` |
| `git commit-tree` with no identity | `fatal: empty ident name`, rc **128** |
| `git gc --prune=now` on an unreferenced `commit-tree` commit | object deleted (`cat-file: could not get object info`); survives when a ref points at it |
| `git mktree` / `read-tree` on Linux with `CON`, `trail.`, `C:evil.txt`, `a\b.txt`, `a:b` | accepted (rc 0); `..`, `.git`, `git~1` rejected as `invalid path` |
| `git checkout-index -a` after a failed `read-tree` | rc **0** and writes nothing — checking only its exit code silently "succeeds" |
| `git clean -xfd` with an ignored docs dir | `Removing docs/`, directory gone |
| `git stash push --all` / `-u` | ignored file present in `stash@{0}^3` / no third parent, file untouched |
| `git ls-tree -r --name-only` without `-z` | C-quoted `"d/\344\270\255…"` and `"d/tab\tname.md"`; `-z` emits raw bytes |
| `git diff-tree -r --name-status -z` | `D\0a\0M\0b\0A\0c\0…`; `--no-renames` turns a move into `D`+`A` |
| `git hash-object --path=f` with `core.autocrlf=true` | blob stores LF (`b77b4eb…`) while the worktree keeps CRLF; `--no-filters` keeps `0aa0f34…` |
| `git hash-object --stdin-paths --path=…` | errors: can't use `--stdin-paths` with `--path` |
| `git push` non-fast-forward | rc **1**, `! [rejected] … (fetch first)`; `--force-with-lease` → `(stale info)` until fetched |
| `git fetch` with `+refs/heads/x:…` after a force-push | rewinds silently (rc 0); a non-forcing refspec reports `! [rejected] … (non-fast-forward)` |
| `git fetch <empty-remote> +refs/heads/main:refs/heads/main` | `fatal: couldn't find remote ref refs/heads/main`, rc **128**; glob refspec `+refs/heads/*:refs/remotes/origin/*` → rc 0 |
| `--path-format` in git 2.30 vs latest docs | absent in 2.30, present in current; added in 2.31 |
| `git hash-object --stdin-paths` | newline-delimited only (no `-z`); safe here because the portable-name rule rejects control characters |
| `git ls-remote --exit-code <url> <missing-ref>` | rc **2**; existing ref → rc 0 |
| `git worktree add` then `rev-parse` | `--absolute-git-dir` = `.git/worktrees/wt`; `--git-common-dir` = `.git`; `--git-path info/exclude` = `.git/info/exclude` |
| `/mnt/c` (9p DrvFs) | `core.ignorecase=true`, `core.filemode=false`, case-insensitive listing confirmed via `CASEPROBE.tmp` |

## Sources

- git-check-ignore(1) — exit statuses and `-v/-n/-z/--no-index` semantics: https://git-scm.com/docs/git-check-ignore
- gitignore(5) — precedence among command line, `.gitignore`, `$GIT_DIR/info/exclude`, `core.excludesFile`: https://git-scm.com/docs/gitignore
- git-rev-parse(1) — `--absolute-git-dir`, `--git-common-dir`, `--git-path`, `--is-shallow-repository`, `--path-format`, `--show-superproject-working-tree`: https://git-scm.com/docs/git-rev-parse
- git-config(1) — `safe.directory`, `core.excludesFile`, `core.symlinks`, `core.fileMode`, `core.ignoreCase`, `core.precomposeUnicode`, `core.quotePath`, `core.protectNTFS`, `core.protectHFS`, `gc.auto`, `gc.pruneExpire`, `credential.interactive`, `http.postBuffer`: https://git-scm.com/docs/git-config
- git(1) — `GIT_TERMINAL_PROMPT`, `GIT_ASKPASS`, `GIT_CONFIG_*`: https://git-scm.com/docs/git
- git-merge-tree(1) — `--write-tree` mode first documented in 2.38.0: https://git-scm.com/docs/git-merge-tree
- git-mktree(1), git-hash-object(1), git-ls-tree(1), git-commit-tree(1), git-update-ref(1), git-fetch(1), git-push(1): https://git-scm.com/docs
- GitHub — file size limits (50 MiB warning, 100 MiB block), repository size guidance: https://docs.github.com/en/repositories/working-with-files/managing-large-files/about-large-files-on-github
- GitHub — push protection scope (repository-level requires Secret Protection and is disabled by default; user-level covers public repositories): https://docs.github.com/en/code-security/concepts/secret-security/push-protection
- Microsoft — reserved names, trailing dot/space, invalid characters, `MAX_PATH` 260: https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file
- Go — `os.Root` traversal-resistant filesystem access (Go 1.24+): https://pkg.go.dev/os#Root ; `os.OpenInRoot` source: https://go.dev/src/os/root.go
- CVE-2026-39822 — `os.Root` path restriction bypass on Unix (keep the toolchain patched): https://access.redhat.com/security/cve/cve-2026-39822
- Empirical verification commands and outputs recorded above (git 2.47.3, WSL2, 2026-10-22); no secondary blog sources were relied on.

---
*Pitfalls research for: doc-connector — private document sync over a shared personal git repository*
*Researched: 2026-10-22*
