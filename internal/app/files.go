package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LZD121/doc-connector/internal/fsx"
)

// parseGitMode turns a tree mode string into a file permission. Windows ignores
// the executable bit, which is why mode equality is normalized there later.
func parseGitMode(mode string) os.FileMode {
	if mode == "100755" {
		return 0o755
	}
	return 0o644
}

func modeString(mode uint32) string { return strconv.FormatUint(uint64(mode), 8) }

// writeAtomicFromPath copies src into docs at rel via a sibling temp file,
// fsync and rename. On failure the temp file is deliberately preserved so a
// partially written document can never replace a good one.
func writeAtomicFromPath(root *fsx.Root, rel, srcPath string, mode os.FileMode) error {
	if err := fsx.ValidateRel(rel); err != nil {
		return err
	}
	parent := path.Dir(rel)
	if parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return err
		}
		if err := rejectSymlinkParents(root, parent); err != nil {
			return err
		}
	}

	tmpRel := path.Join(parent, "."+path.Base(rel)+".tmp-"+randomSuffix())
	if parent == "." {
		tmpRel = "." + path.Base(rel) + ".tmp-" + randomSuffix()
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open staged blob: %w", err)
	}
	defer src.Close()

	dst, err := root.OpenFile(tmpRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("write temporary file %q (kept for diagnosis): %w", tmpRel, err)
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return fmt.Errorf("fsync temporary file %q (kept for diagnosis): %w", tmpRel, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close temporary file %q (kept for diagnosis): %w", tmpRel, err)
	}
	if err := root.Rename(tmpRel, rel); err != nil {
		return fmt.Errorf("replace %q via %q (temporary file kept): %w", rel, tmpRel, err)
	}
	return nil
}

// rejectSymlinkParents refuses a write whose parent chain contains a symlink.
// os.Root would follow an in-root link; a document must never be written
// through an alias the user did not intend.
func rejectSymlinkParents(root *fsx.Root, parent string) error {
	cur := ""
	for _, part := range strings.Split(parent, "/") {
		if part == "" || part == "." {
			continue
		}
		cur = path.Join(cur, part)
		info, err := root.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return &fsx.Error{Msg: fmt.Sprintf("refusing to write through symlink %q", cur)}
		}
		if !info.IsDir() {
			return &fsx.Error{Msg: fmt.Sprintf("parent %q is not a directory", cur)}
		}
	}
	return nil
}

// moveToTrash removes src by moving it into the operation's trash directory.
// A cross-device rename falls back to copy+remove; a failed copy leaves src
// untouched.
func moveToTrash(src, trash string) error {
	if err := os.MkdirAll(filepath.Dir(trash), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, trash); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(trash, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(trash)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(trash)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(trash)
		return err
	}
	return os.Remove(src)
}

// cleanupEmptyDirs removes now-empty parents of deleted paths, never crossing
// above the docs root.
func cleanupEmptyDirs(docsDir string, rels []string) {
	seen := map[string]struct{}{}
	for _, rel := range rels {
		dir := path.Dir(rel)
		for dir != "." && dir != "/" {
			if _, ok := seen[dir]; ok {
				break
			}
			seen[dir] = struct{}{}
			if err := os.Remove(filepath.Join(docsDir, filepath.FromSlash(dir))); err != nil {
				break
			}
			dir = path.Dir(dir)
		}
	}
}

func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(b[:])
}

// writeAtomicAbs writes src into an absolute destination via a sibling temp
// file. Used by restore --to, where the target is outside the docs root.
func writeAtomicAbs(target, src string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.CreateTemp(filepath.Dir(target), ".tmp-"+filepath.Base(target)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Chmod(mode); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := io.Copy(f, in); err != nil {
		f.Close()
		return fmt.Errorf("write temporary file %q (kept): %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("fsync %q (kept): %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q (kept): %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("rename %q -> %q (temporary file kept): %w", tmp, target, err)
	}
	return nil
}
