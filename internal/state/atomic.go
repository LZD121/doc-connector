package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data in a crash-safe way: write a sibling
// temporary file, fsync it, rename over the target, then best-effort fsync the
// directory. A crash leaves either the old complete file or the new complete
// file, never a truncated one.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tmp := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	if err := f.Chmod(mode); err != nil {
		cleanup()
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	syncDir(dir)
	return nil
}

// syncDir flushes directory metadata where the platform supports it. Windows
// rejects directory Sync; that is not a failure for our guarantees.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// ReadJSON decodes a JSON file into v. A missing file yields os.ErrNotExist.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// WriteJSON encodes v atomically with mode 0600.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return WriteFileAtomic(path, data, 0o600)
}

// IsNotExist reports whether err means "the file is absent".
func IsNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
