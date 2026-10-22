package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/state"
)

// LogOptions selects history entries.
type LogOptions struct {
	Path  string
	Limit int
}

// LogEntry is one commit summary.
type LogEntry struct {
	OID     string
	At      string
	Author  string
	Machine string
	Subject string
}

// LogResult is an offline history listing.
type LogResult struct {
	Entries []LogEntry
	Fetched state.Fetched
}

// Log reads the local mirror only: it never contacts the remote. Refresh the
// mirror with status or pull first.
func Log(ctx context.Context, env Env, opts LogOptions) (*LogResult, error) {
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: false})
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	ref := gitstore.RemoteBranch(rc.Config.Branch)
	tip, err := rc.Store.RefTip(ctx, ref)
	if err != nil {
		return nil, wrapErr(err, 1)
	}
	if tip == "" {
		return nil, errf(codeUsage, "no fetched history for branch %q", rc.Config.Branch).
			withHint("run \"doc-connector status\" or \"doc-connector pull\" once while online")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	// A record separator (0x1e) is required: trailer values end with a newline,
	// so newline-delimited records would split a commit into two lines.
	format := "%H%x00%aI%x00%an%x00%s%x00%B%x1e"
	args := []string{"log", "--format=" + format, "--max-count", fmt.Sprint(limit), ref}
	if opts.Path != "" {
		entry, rel, perr := rc.resolveUserPath(opts.Path)
		if perr != nil {
			return nil, perr
		}
		if err := validatePath(rel, entry.Budget); err != nil {
			return nil, err
		}
		args = append(args, "--", rc.Config.Project.ID+"/"+projectPath(entry.Mapping.Prefix, rel))
	} else {
		args = append(args, "--", rc.Config.Project.ID+"/")
	}

	out, err := rc.Store.Run(ctx, args...)
	if err != nil {
		return nil, wrapErr(err, 1)
	}

	res := &LogResult{Fetched: state.LoadFetched(rc.Repo.GitDir)}
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		fields := strings.Split(rec, "\x00")
		if len(fields) < 5 {
			continue
		}
		res.Entries = append(res.Entries, LogEntry{
			OID:     fields[0],
			At:      fields[1],
			Author:  fields[2],
			Subject: fields[3],
			Machine: trailerValue(fields[4], "Doc-Connector-Machine"),
		})
	}
	return res, nil
}

func trailerValue(body, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func validateUserPath(rel string, rc *RepoContext) error {
	budget := rc.Config.Limits.MaxPathChars - len(rc.Config.DocsDir) - 1
	if err := validatePath(rel, budget); err != nil {
		return err
	}
	return nil
}
