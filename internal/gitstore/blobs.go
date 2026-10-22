package gitstore

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WriteBlobs streams every OID from the store into dir (one file per OID) using
// a single cat-file --batch process. Contents never pass through memory as a
// whole. It returns oid -> absolute path.
func (s *Store) WriteBlobs(ctx context.Context, oids []string, dir string) (map[string]string, error) {
	result := make(map[string]string, len(oids))
	if len(oids) == 0 {
		return result, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	unique := make([]string, 0, len(oids))
	seen := map[string]struct{}{}
	for _, oid := range oids {
		if _, ok := seen[oid]; ok {
			continue
		}
		seen[oid] = struct{}{}
		unique = append(unique, oid)
	}

	args := []string{"--git-dir=" + s.Dir, "cat-file", "--batch"}
	cmd := s.Runner.Cmd(ctx, args...)
	cmd.Stdin = strings.NewReader(strings.Join(unique, "\n") + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	br := bufio.NewReaderSize(stdout, 64*1024)
	for _, oid := range unique {
		header, rerr := br.ReadString('\n')
		if rerr != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("read cat-file header for %s: %w", oid, rerr)
		}
		fields := strings.Fields(header)
		if len(fields) == 2 && fields[1] == "missing" {
			_ = cmd.Wait()
			return nil, &Error{
				Args:   args,
				Code:   -1,
				Stderr: fmt.Sprintf("object %s is missing from the mirror", oid),
				Kind:   KindOther,
			}
		}
		if len(fields) != 3 {
			_ = cmd.Wait()
			return nil, fmt.Errorf("unexpected cat-file header %q", strings.TrimSpace(header))
		}
		size, perr := strconv.ParseInt(fields[2], 10, 64)
		if perr != nil || size < 0 {
			_ = cmd.Wait()
			return nil, fmt.Errorf("invalid blob size in %q", strings.TrimSpace(header))
		}

		path := filepath.Join(dir, oid)
		f, cerr := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if cerr != nil {
			_ = cmd.Wait()
			return nil, cerr
		}
		if _, cerr := io.CopyN(f, br, size); cerr != nil {
			f.Close()
			_ = cmd.Wait()
			return nil, fmt.Errorf("write blob %s: %w", oid, cerr)
		}
		if cerr := f.Sync(); cerr != nil {
			f.Close()
			_ = cmd.Wait()
			return nil, cerr
		}
		if cerr := f.Close(); cerr != nil {
			_ = cmd.Wait()
			return nil, cerr
		}
		// Trailing newline after the content.
		if _, cerr := br.ReadByte(); cerr != nil {
			_ = cmd.Wait()
			return nil, cerr
		}
		result[oid] = path
	}

	if err := cmd.Wait(); err != nil {
		return nil, &Error{
			Args:   args,
			Code:   -1,
			Stderr: stderr.String(),
			Kind:   Classify(stderr.String(), -1),
			Err:    err,
		}
	}
	return result, nil
}
