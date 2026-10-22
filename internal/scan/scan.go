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
	"os"
	"path"
	"path/filepath"
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
	// FollowSymlinks synchronizes the bytes of a link's target under the link's
	// own path. Targets must resolve to a regular file inside the docs root;
	// the remote never receives a 120000 entry.
	FollowSymlinks bool
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
		// key is the path in the tree; hash is the path whose bytes git
		// hashes, relative to docsDir. They differ only for symlinks.
		key  string
		hash string
		mode uint32
	}
	var files []candidate
	var offenders []string

	var checkSize = func(rel string, size int64) error {
		if opts.MaxBytes > 0 && size > opts.MaxBytes {
			return &Error{
				Code: 1,
				Msg:  fmt.Sprintf("file %q is %d bytes, above the %d byte hard limit", rel, size, opts.MaxBytes),
				Hint: "move large files out of the docs directory; GitHub rejects blobs this large",
			}
		}
		if opts.WarnBytes > 0 && size > opts.WarnBytes && !opts.Force {
			return &Error{
				Code: 1,
				Msg:  fmt.Sprintf("file %q is %d bytes, above the %d byte warning limit", rel, size, opts.WarnBytes),
				Hint: "re-run with --force to synchronize it anyway",
			}
		}
		return nil
	}

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
			if !opts.FollowSymlinks {
				res.Skipped = append(res.Skipped, rel)
				return nil
			}
			resolved, size, mode, problem := resolveLink(root.Dir(), rel)
			if problem != "" {
				offenders = append(offenders, problem)
				return nil
			}
			if err := checkSize(rel, size); err != nil {
				return err
			}
			res.TotalBytes += size
			files = append(files, candidate{key: rel, hash: resolved, mode: mode})
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
		if err := checkSize(rel, info.Size()); err != nil {
			return err
		}
		res.TotalBytes += info.Size()
		mode := uint32(0o100644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o100755
		}
		files = append(files, candidate{key: rel, hash: rel, mode: mode})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// A refused link fails the whole scan instead of silently disappearing:
	// a sync that quietly drops a path is worse than one that stops.
	if len(offenders) > 0 {
		return nil, &Error{
			Code: 3,
			Msg:  fmt.Sprintf("%d symbolic link(s) cannot be followed: %s", len(offenders), strings.Join(offenders, "; ")),
			Hint: "make each link point to a regular file inside the docs directory, or run without --follow-symlinks to skip symbolic links",
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].key < files[j].key })
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.hash
	}
	oids, err := store.HashPaths(ctx, docsDir, names, opts.Write)
	if err != nil {
		return nil, err
	}
	for i, f := range files {
		res.Tree[f.key] = trees.Entry{Mode: f.mode, OID: strings.TrimSpace(oids[i])}
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

// resolveLink resolves one symbolic link to a hashable path inside rootDir and
// returns that path, the target size and the regular-file mode. problem is
// non-empty when the link must be refused; the empty values are then unused.
func resolveLink(rootDir, rel string) (resolved string, size int64, mode uint32, problem string) {
	raw, err := os.Readlink(filepath.Join(rootDir, filepath.FromSlash(rel)))
	if err != nil {
		return "", 0, 0, fmt.Sprintf("%s: cannot read link target (%v)", rel, err)
	}
	slashTarget := filepath.ToSlash(raw)
	if filepath.IsAbs(raw) || path.IsAbs(slashTarget) {
		return "", 0, 0, fmt.Sprintf("%s -> %s: absolute targets are not allowed", rel, raw)
	}
	clean := path.Clean(path.Join(path.Dir(rel), slashTarget))
	if err := fsx.ValidateRel(clean); err != nil {
		return "", 0, 0, fmt.Sprintf("%s -> %s: target escapes the docs directory", rel, raw)
	}
	abs := filepath.Join(rootDir, filepath.FromSlash(clean))
	info, err := os.Stat(abs)
	if err != nil {
		return "", 0, 0, fmt.Sprintf("%s -> %s: target is missing or unreadable (%v)", rel, raw, err)
	}
	if info.IsDir() {
		return "", 0, 0, fmt.Sprintf("%s -> %s: target is a directory", rel, raw)
	}
	if !info.Mode().IsRegular() {
		return "", 0, 0, fmt.Sprintf("%s -> %s: target is not a regular file", rel, raw)
	}
	// A chain of links can still leave the docs root even when every single
	// target is relative; compare real paths before reading the bytes.
	realRoot, rerr := filepath.EvalSymlinks(rootDir)
	realTarget, terr := filepath.EvalSymlinks(abs)
	if rerr != nil || terr != nil {
		return "", 0, 0, fmt.Sprintf("%s -> %s: cannot resolve the target path", rel, raw)
	}
	back, berr := filepath.Rel(realRoot, realTarget)
	if berr != nil || !filepath.IsLocal(back) {
		return "", 0, 0, fmt.Sprintf("%s -> %s: target resolves outside the docs directory", rel, raw)
	}

	mode = 0o100644
	if info.Mode().Perm()&0o111 != 0 {
		mode = 0o100755
	}
	return clean, info.Size(), mode, ""
}
