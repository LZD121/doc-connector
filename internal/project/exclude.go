package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LZD121/doc-connector/internal/state"
)

// ExcludeBegin and ExcludeEnd delimit the managed block. Everything outside the
// block belongs to the user and is preserved byte-for-byte.
const (
	ExcludeBegin = "# doc-connector:begin"
	ExcludeEnd   = "# doc-connector:end"
)

// ExcludePath returns <common-dir>/info/exclude.
func ExcludePath(commonDir string) string {
	return filepath.Join(commonDir, "info", "exclude")
}

// ExcludeBlock renders the managed block for docsDir (slash-separated, anchored
// at the worktree root, spaces escaped).
func ExcludeBlock(docsDir string) string {
	return ExcludeBlockDirs([]string{docsDir})
}

// ExcludeBlockDirs renders one managed block covering every mapped docs
// directory: one anchored pattern per directory, in the given order.
func ExcludeBlockDirs(docsDirs []string) string {
	var b strings.Builder
	b.WriteString(ExcludeBegin + "\n")
	for _, dir := range docsDirs {
		p := strings.Trim(filepath.ToSlash(dir), "/")
		p = strings.ReplaceAll(p, " ", `\ `)
		fmt.Fprintf(&b, "/%s/\n", p)
	}
	b.WriteString(ExcludeEnd + "\n")
	return b.String()
}

// EnsureExclude makes the exclude file contain exactly one managed block for
// docsDir. It reports whether the file changed.
//
// The function never touches .gitignore: sharing a team repository means the
// only writable ignore source is the local, untracked info/exclude.
func EnsureExclude(excludePath, docsDir string) (bool, error) {
	return EnsureExcludeDirs(excludePath, []string{docsDir})
}

// EnsureExcludeDirs is EnsureExclude for a multi-directory project: the block
// lists every mapped directory so each one is hidden from the team repository.
func EnsureExcludeDirs(excludePath string, docsDirs []string) (bool, error) {
	original, mode, err := readExclude(excludePath)
	if err != nil {
		return false, err
	}
	block := ExcludeBlockDirs(docsDirs)

	content := original
	begin := strings.Index(content, ExcludeBegin)
	if begin >= 0 {
		if begin != 0 && content[begin-1] != '\n' {
			return false, fmt.Errorf("%s: managed marker is not at the start of a line; refusing to rewrite", excludePath)
		}
		rest := content[begin:]
		endRel := strings.Index(rest, ExcludeEnd)
		if endRel < 0 {
			return false, fmt.Errorf("%s: managed block has no end marker; refusing to rewrite", excludePath)
		}
		end := begin + endRel + len(ExcludeEnd)
		// Consume the end-marker's line ending, if any.
		if end < len(content) {
			if content[end] == '\r' {
				end++
			}
			if end < len(content) && content[end] == '\n' {
				end++
			}
		}
		content = content[:begin] + block + content[end:]
	} else {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += block
	}

	if content == original {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return false, err
	}
	if err := state.WriteFileAtomic(excludePath, []byte(content), mode); err != nil {
		return false, err
	}
	return true, nil
}

func readExclude(path string) (string, os.FileMode, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", 0o644, nil
		}
		return "", 0, err
	}
	mode := os.FileMode(0o644)
	if info, serr := os.Stat(path); serr == nil {
		mode = info.Mode().Perm()
	}
	return string(data), mode, nil
}
