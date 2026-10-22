// Package scan turns the docs directory into a content-addressed tree.
//
// Nothing but git decides equality: paths are hashed with
// `hash-object --no-filters`, so touching a file or copying identical bytes
// never produces a false change, and a same-size edit is never missed.
package scan

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/LZD121/doc-connector/internal/fsx"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/ignore"
	"github.com/LZD121/doc-connector/internal/trees"
)

// Options controls hashing and filtering.
type Options struct {
	// Write stores blobs in the mirror. status uses false; push uses true.
	Write  bool
	Filter *ignore.Filter
	// MaxBytes rejects oversized files; 0 disables the check.
	MaxBytes int64
	// WarnBytes requires Force above this size; 0 disables the warning tier.
	WarnBytes int64
	// Force allows files above WarnBytes.
	Force bool
}

// Result is a scan outcome.
type Result struct {
	Tree       trees.Tree
	Ignored    int
	Skipped    []string
	TotalBytes int64
}

// Error is a scan failure with an exit code.
type Error struct {
	Code int
	Msg  string
	Hint string
	Err  error
}

func (e *Error) Error() string    { return e.Msg }
func (e *Error) Unwrap() error    { return e.Err }
func (e *Error) ExitCode() int    { return e.Code }
func (e *Error) HintText() string { return e.Hint }

// Scan walks docsDir and returns the tree of regular files.
func Scan(ctx context.Context, store *gitstore.Store, docsDir string, opts Options) (*Result, error) {
	root, err := fsx.Open(docsDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	if opts.Filter == nil {
		opts.Filter = ignore.New(nil)
	}

	res := &Result{Tree: trees.Tree{}}
	type candidate struct {
		rel  string
		mode uint32
	}
	var files []candidate

	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." {
			return nil
		}
		rel := path.Clean(p)
		if opts.Filter.Match(rel, d.IsDir()) {
			res.Ignored++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			res.Skipped = append(res.Skipped, rel)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			res.Skipped = append(res.Skipped, rel)
			return nil
		}
		if err := fsx.ValidateRel(rel); err != nil {
			return err
		}
		if opts.MaxBytes > 0 && info.Size() > opts.MaxBytes {
			return &Error{
				Code: 1,
				Msg:  fmt.Sprintf("file %q is %d bytes, above the %d byte hard limit", rel, info.Size(), opts.MaxBytes),
				Hint: "move large files out of the docs directory; GitHub rejects blobs this large",
			}
		}
		if opts.WarnBytes > 0 && info.Size() > opts.WarnBytes && !opts.Force {
			return &Error{
				Code: 1,
				Msg:  fmt.Sprintf("file %q is %d bytes, above the %d byte warning limit", rel, info.Size(), opts.WarnBytes),
				Hint: "re-run with --force to synchronize it anyway",
			}
		}
		res.TotalBytes += info.Size()
		mode := uint32(0o100644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o100755
		}
		files = append(files, candidate{rel: rel, mode: mode})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.rel
	}
	oids, err := store.HashPaths(ctx, docsDir, names, opts.Write)
	if err != nil {
		return nil, err
	}
	for i, f := range files {
		res.Tree[f.rel] = trees.Entry{Mode: f.mode, OID: strings.TrimSpace(oids[i])}
	}
	sort.Strings(res.Skipped)
	paths := make([]string, 0, len(res.Tree))
	for p := range res.Tree {
		paths = append(paths, p)
	}
	if err := fsx.CheckCaseCollisions(paths); err != nil {
		return nil, err
	}
	return res, nil
}
