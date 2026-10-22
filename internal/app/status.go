package app

import (
	"context"
	"fmt"
	"runtime"

	"github.com/LZD121/doc-connector/internal/state"
	"github.com/LZD121/doc-connector/internal/trees"
)

// StatusResult is a read-only three-way report.
type StatusResult struct {
	Summary       trees.Summary
	Actions       []trees.Action
	Ignored       int
	Skipped       []string
	RemoteTip     string
	RemoteChecked bool
	Fetched       state.Fetched
	FetchError    error

	// Facts echoed by the JSON document; the human renderer does not use them.
	ProjectID         string
	DocsDir           string
	Remote            string
	Branch            string
	BaseCommit        string
	BaseTree          string
	FetchedAgeSeconds int64
}

// Status compares baseline, local and remote. It never writes state.json.
//
// When the remote cannot be reached the report is still produced from local
// facts and the cache; the returned error carries exit code 4 so the caller can
// print the report first and then exit non-zero.
func Status(ctx context.Context, env Env, offline bool) (*StatusResult, error) {
	rc, err := Load(ctx, env, LoadOptions{CreateDocs: false})
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	// status is read-only: it reports instead of refusing on size, and the
	// classification below wins the case/NFC check.
	local, err := rc.ScanLocal(ctx, ScanPolicy{})
	if err != nil {
		return nil, err
	}

	fetched := state.LoadFetched(rc.Repo.GitDir)
	res := &StatusResult{
		Ignored:    local.Ignored,
		Skipped:    local.Skipped,
		Fetched:    fetched,
		ProjectID:  rc.Config.Project.ID,
		DocsDir:    rc.Config.DocsDir,
		Remote:     rc.Config.Remote,
		Branch:     rc.Config.Branch,
		BaseCommit: rc.State.SyncedCommit,
		BaseTree:   rc.State.BaselineTree,
	}

	tip := fetched.Commit
	if !offline {
		got, ferr := rc.Fetch(ctx, "status")
		if ferr != nil {
			res.FetchError = ferr
		} else {
			tip = got
			res.RemoteChecked = true
			res.Fetched = state.LoadFetched(rc.Repo.GitDir)
		}
	}
	res.RemoteTip = tip

	base, err := rc.Baseline(ctx)
	if err != nil {
		return nil, err
	}
	remote, _, err := rc.RemoteTree(ctx, tip)
	if err != nil {
		return nil, err
	}
	localTree := local.Tree
	if runtime.GOOS == "windows" {
		localTree = trees.NormalizeModes(localTree, base, remote)
	}
	res.Actions, res.Summary = trees.Classify(base, localTree, remote)
	res.FetchedAgeSeconds = FetchedAgeSeconds(res.Fetched)

	if res.FetchError != nil {
		return res, &Error{
			Code: codeRemote,
			Msg:  fmt.Sprintf("remote not checked: %v", res.FetchError),
			Hint: "the report below reflects local and cached remote state; retry when the network or credentials are available",
			Err:  res.FetchError,
		}
	}
	return res, nil
}

// ActionLines renders one line per path, using the classification status.
func ActionLines(actions []trees.Action) []string {
	lines := make([]string, 0, len(actions))
	for _, a := range actions {
		status := a.Status
		if status == "" {
			status = " "
		}
		lines = append(lines, fmt.Sprintf("%s %s", status, a.Path))
	}
	return lines
}

// RemoteLabel formats the remote tip and its age.
func RemoteLabel(res *StatusResult) string {
	label := "remote: not checked"
	if res.RemoteTip != "" {
		label = "remote: " + shortOID(res.RemoteTip)
	}
	if age := FetchedAge(res.Fetched); age != "" {
		label += " (" + age + ")"
	}
	if res.RemoteChecked {
		if res.RemoteTip == "" {
			label = "remote: empty"
		} else {
			label = "remote: " + shortOID(res.RemoteTip)
		}
		if age := FetchedAge(res.Fetched); age != "" {
			label += " (fetched " + age + ")"
		}
	}
	return label
}

// ConflictPaths lists conflicting paths.
func ConflictPaths(actions []trees.Action) []string {
	var out []string
	for _, a := range actions {
		if a.Status == "C" {
			out = append(out, a.Path)
		}
	}
	return out
}
