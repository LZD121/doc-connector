// Package fsx confines every docs-directory read and write to an os.Root.
//
// os.Root is an accident-prevention boundary, not a security sandbox: hard
// links can still point outside the tree. It does guarantee that path traversal
// and absolute paths cannot escape the docs directory.
package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Error is a path-safety failure (exit 3).
type Error struct {
	Msg  string
	Hint string
	Err  error
}

func (e *Error) Error() string    { return e.Msg }
func (e *Error) Unwrap() error    { return e.Err }
func (e *Error) ExitCode() int    { return 3 }
func (e *Error) HintText() string { return e.Hint }

// Root wraps os.Root with doc-connector's path rules.
type Root struct {
	root *os.Root
	dir  string
}

// Open opens dir as a confined root. A symlink at the docs path is refused:
// following it would sync content from outside the intended tree.
func Open(dir string) (*Root, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, &Error{Msg: fmt.Sprintf("docs directory %q is not accessible: %v", dir, err), Err: err}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, &Error{
			Msg:  fmt.Sprintf("docs directory %q is a symbolic link", dir),
			Hint: "point docs_dir at a real directory; doc-connector never follows the docs root symlink",
		}
	}
	if !info.IsDir() {
		return nil, &Error{Msg: fmt.Sprintf("docs path %q is not a directory", dir)}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, &Error{Msg: fmt.Sprintf("cannot open docs root %q: %v", dir, err), Err: err}
	}
	return &Root{root: root, dir: dir}, nil
}

// Close releases the root handle.
func (r *Root) Close() error { return r.root.Close() }

// Dir returns the absolute docs directory.
func (r *Root) Dir() string { return r.dir }

// FS returns the root as an fs.FS for fs.WalkDir.
func (r *Root) FS() fs.FS { return r.root.FS() }

// Check validates a relative path before it reaches the OS.
func (r *Root) Check(rel string) error {
	return ValidateRel(rel)
}

// OpenFile opens rel inside the root.
func (r *Root) OpenFile(rel string, flag int, perm os.FileMode) (*os.File, error) {
	if err := ValidateRel(rel); err != nil {
		return nil, err
	}
	f, err := r.root.OpenFile(filepath.FromSlash(rel), flag, perm)
	if err != nil {
		return nil, wrap(rel, err)
	}
	return f, nil
}

// ReadFile reads rel inside the root.
func (r *Root) ReadFile(rel string) ([]byte, error) {
	if err := ValidateRel(rel); err != nil {
		return nil, err
	}
	data, err := r.root.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		return nil, wrap(rel, err)
	}
	return data, nil
}

// WriteFile writes rel inside the root (O_CREATE|O_TRUNC).
func (r *Root) WriteFile(rel string, data []byte, perm os.FileMode) error {
	if err := ValidateRel(rel); err != nil {
		return err
	}
	if err := r.root.WriteFile(filepath.FromSlash(rel), data, perm); err != nil {
		return wrap(rel, err)
	}
	return nil
}

// MkdirAll creates a directory chain inside the root.
func (r *Root) MkdirAll(rel string, perm os.FileMode) error {
	if err := ValidateRel(rel); err != nil && rel != "." {
		return err
	}
	if err := r.root.MkdirAll(filepath.FromSlash(rel), perm); err != nil {
		return wrap(rel, err)
	}
	return nil
}

// Rename renames within the root.
func (r *Root) Rename(oldRel, newRel string) error {
	if err := ValidateRel(oldRel); err != nil {
		return err
	}
	if err := ValidateRel(newRel); err != nil {
		return err
	}
	if err := r.root.Rename(filepath.FromSlash(oldRel), filepath.FromSlash(newRel)); err != nil {
		return wrap(oldRel, err)
	}
	return nil
}

// Remove deletes a file inside the root.
func (r *Root) Remove(rel string) error {
	if err := ValidateRel(rel); err != nil {
		return err
	}
	if err := r.root.Remove(filepath.FromSlash(rel)); err != nil {
		return wrap(rel, err)
	}
	return nil
}

// Lstat stats rel without following a final symlink.
func (r *Root) Lstat(rel string) (os.FileInfo, error) {
	if err := ValidateRel(rel); err != nil {
		return nil, err
	}
	info, err := r.root.Lstat(filepath.FromSlash(rel))
	if err != nil {
		return nil, wrap(rel, err)
	}
	return info, nil
}

// Stat stats rel.
func (r *Root) Stat(rel string) (os.FileInfo, error) {
	if err := ValidateRel(rel); err != nil {
		return nil, err
	}
	info, err := r.root.Stat(filepath.FromSlash(rel))
	if err != nil {
		return nil, wrap(rel, err)
	}
	return info, nil
}

// wrap keeps ordinary I/O failures as plain errors so the application layer
// maps them to exit 1. Exit 3 is reserved for path-safety violations.
func wrap(rel string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return err
	}
	return fmt.Errorf("docs path %q: %w", rel, err)
}
