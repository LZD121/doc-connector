package gitstore

import (
	"context"
	"strings"
)

// HashPaths hashes paths (relative to workDir) into the store and returns the
// OIDs in the same order. With write=false it computes OIDs without storing
// objects, which is what status uses.
//
// Batch input is newline-separated, so paths containing a newline are refused
// by the caller's path rules; there is no per-file fallback to get wrong.
func (s *Store) HashPaths(ctx context.Context, workDir string, paths []string, write bool) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	local := *s.Runner
	local.Dir = workDir

	args := []string{"--git-dir=" + s.Dir, "hash-object"}
	if write {
		args = append(args, "-w")
	}
	args = append(args, "--no-filters", "--stdin-paths")

	cmd := local.Cmd(ctx, args...)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, &Error{
			Args:   args,
			Code:   -1,
			Stderr: stderr.String(),
			Kind:   Classify(stderr.String(), -1),
			Err:    err,
		}
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != len(paths) {
		return nil, &Error{
			Args:   args,
			Code:   -1,
			Stderr: "hash-object returned an unexpected number of OIDs",
			Kind:   KindOther,
		}
	}
	return lines, nil
}

// BlobSize returns the uncompressed size of a blob.
func (s *Store) BlobSize(ctx context.Context, oid string) (int64, error) {
	out, err := s.Run(ctx, "cat-file", "-s", oid)
	if err != nil {
		return 0, err
	}
	out = strings.TrimSpace(out)
	var n int64
	for _, r := range out {
		if r < '0' || r > '9' {
			return 0, &Error{Args: []string{"cat-file", "-s", oid}, Code: -1, Stderr: "invalid blob size", Kind: KindOther}
		}
		n = n*10 + int64(r-'0')
	}
	return n, nil
}
