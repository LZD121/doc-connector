---
phase: 09-distribution-and-completion
plan: 03
subsystem: cli
tags: [go, completion, bash, zsh, fish]
provides:
  - "doc-connector completion bash|zsh|fish from one table"
key-files:
  created: [internal/cli/completion.go, internal/cli/completion_test.go]
  modified: [internal/cli/cli.go, internal/cli/help.go]
key-decisions:
  - "One completionTable drives all three renderers, so a new flag cannot appear in one shell only"
  - "Static scripts: no completion framework, no runtime introspection"
duration: 12min
completed: 2026-10-23
status: complete
---

# Plan 09-03 Summary

**Tab completion knows every command and every v2 flag.**

## Accomplishments
- `TestCompletionCoversEveryCommand` walks the table per shell and fails if any command or flag is missing from any script.
- Unsupported shells exit 2 with a hint listing bash/zsh/fish; `completion` with no argument is a usage error.
