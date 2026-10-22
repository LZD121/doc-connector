package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

// completionCommand is one row of the shell-completion table. It is the single
// source for all three shells, so a new flag cannot be described in bash and
// forgotten in zsh.
type completionCommand struct {
	Name  string
	Desc  string
	Flags []string
}

var completionTable = []completionCommand{
	{Name: "init", Desc: "Bind this repository to a private remote", Flags: []string{"--remote", "--project-id", "--branch", "--docs-dir", "--machine", "--force", "--help"}},
	{Name: "status", Desc: "Compare local docs, baseline and remote", Flags: []string{"--offline", "--json", "--follow-symlinks", "--root", "--help"}},
	{Name: "push", Desc: "Publish the local docs directory", Flags: []string{"--dry-run", "--resolve", "--force", "--allow-large", "--follow-symlinks", "--json", "--help"}},
	{Name: "pull", Desc: "Apply remote changes to the docs directory", Flags: []string{"--dry-run", "--force", "--allow-large", "--follow-symlinks", "--json", "--help"}},
	{Name: "log", Desc: "Show sync history from the local mirror", Flags: []string{"--path", "--limit", "--help"}},
	{Name: "restore", Desc: "Write a historical version of a path", Flags: []string{"--from", "--to", "--force", "--help"}},
	{Name: "doctor", Desc: "Diagnose the repository without changing it", Flags: []string{"--json", "--help"}},
	{Name: "diff", Desc: "Show a historical diff for this project", Flags: []string{"--from", "--json", "--help"}},
	{Name: "sync", Desc: "Sync every repository under a directory", Flags: []string{"--root", "--dry-run", "--fail-fast", "--json", "--help"}},
	{Name: "tui", Desc: "Interactive workspace screen with conflict triage", Flags: []string{"--root", "--help"}},
	{Name: "completion", Desc: "Print a shell completion script", Flags: []string{"--help"}},
}

const completionUsage = `Usage:
  doc-connector completion <bash|zsh|fish>

Prints a completion script for the given shell to stdout. Enable it with, for
example: eval "$(doc-connector completion bash)"

Flags:
  -h, --help   Show this help
`

func cmdCompletion(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	helped, err := parseFlags(fs, args, e.Stdout, completionUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if fs.NArg() != 1 {
		return Errorf(CodeUsage, "completion: expected exactly one shell name").
			WithHint("run \"doc-connector completion bash\", \"... zsh\" or \"... fish\"")
	}
	shell := fs.Arg(0)
	script, ok := completionScript(shell)
	if !ok {
		return Errorf(CodeUsage, "completion: unsupported shell %q", shell).
			WithHint("supported shells are bash, zsh and fish")
	}
	fmt.Fprint(e.Stdout, script)
	return nil
}

func completionScript(shell string) (string, bool) {
	switch shell {
	case "bash":
		return bashCompletion(), true
	case "zsh":
		return zshCompletion(), true
	case "fish":
		return fishCompletion(), true
	default:
		return "", false
	}
}

func completionNames() string {
	names := make([]string, 0, len(completionTable))
	for _, c := range completionTable {
		names = append(names, c.Name)
	}
	return strings.Join(names, " ")
}

func bashCompletion() string {
	var b strings.Builder
	b.WriteString("# bash completion for doc-connector\n")
	b.WriteString("# enable: eval \"$(doc-connector completion bash)\"\n")
	b.WriteString("_doc_connector() {\n")
	b.WriteString("  local cur cmd\n")
	b.WriteString("  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("  if [ \"$COMP_CWORD\" -eq 1 ]; then\n")
	fmt.Fprintf(&b, "    COMPREPLY=( $(compgen -W %q -- \"$cur\") )\n", completionNames()+" --help --version")
	b.WriteString("    return 0\n")
	b.WriteString("  fi\n")
	b.WriteString("  cmd=\"${COMP_WORDS[1]}\"\n")
	b.WriteString("  case \"$cmd\" in\n")
	for _, c := range completionTable {
		fmt.Fprintf(&b, "    %s) COMPREPLY=( $(compgen -W %q -- \"$cur\") ) ;;\n", c.Name, strings.Join(c.Flags, " "))
	}
	b.WriteString("    *) COMPREPLY=() ;;\n")
	b.WriteString("  esac\n")
	b.WriteString("  return 0\n")
	b.WriteString("}\n")
	b.WriteString("complete -F _doc_connector doc-connector\n")
	return b.String()
}

func zshCompletion() string {
	var b strings.Builder
	b.WriteString("#compdef doc-connector\n")
	b.WriteString("# zsh completion for doc-connector\n")
	b.WriteString("# enable: eval \"$(doc-connector completion zsh)\"\n")
	b.WriteString("_doc_connector() {\n")
	b.WriteString("  local -a commands\n")
	b.WriteString("  commands=(\n")
	for _, c := range completionTable {
		fmt.Fprintf(&b, "    '%s:%s'\n", c.Name, c.Desc)
	}
	b.WriteString("  )\n")
	b.WriteString("  if (( CURRENT == 2 )); then\n")
	b.WriteString("    _describe 'command' commands\n")
	b.WriteString("    return\n")
	b.WriteString("  fi\n")
	b.WriteString("  case $words[2] in\n")
	for _, c := range completionTable {
		fmt.Fprintf(&b, "    %s) _arguments %s ;;\n", c.Name, quoteZshFlags(c.Flags))
	}
	b.WriteString("    *) _files ;;\n")
	b.WriteString("  esac\n")
	b.WriteString("}\n")
	b.WriteString("_doc_connector \"$@\"\n")
	return b.String()
}

// quoteZshFlags renders flags as _arguments specs. A flag that takes a value is
// written as '--flag=[value]', which also completes the flag itself.
func quoteZshFlags(flags []string) string {
	parts := make([]string, 0, len(flags))
	for _, f := range flags {
		parts = append(parts, fmt.Sprintf("'%s'", f))
	}
	return strings.Join(parts, " ")
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# fish completion for doc-connector\n")
	b.WriteString("# enable: doc-connector completion fish | source\n")
	fmt.Fprintf(&b, "complete -c doc-connector -f -n '__fish_use_subcommand' -a %q\n", completionNames())
	b.WriteString("complete -c doc-connector -n '__fish_use_subcommand' -l help -d 'Show help'\n")
	b.WriteString("complete -c doc-connector -n '__fish_use_subcommand' -l version -d 'Show the version'\n")
	for _, c := range completionTable {
		for _, f := range c.Flags {
			name := strings.TrimPrefix(f, "--")
			if name == "help" {
				continue
			}
			fmt.Fprintf(&b, "complete -c doc-connector -n '__fish_seen_subcommand_from %s' -l %s\n", c.Name, name)
		}
	}
	return b.String()
}
