package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LZD121/doc-connector/internal/state"
)

// discoverDepth bounds the walk: the root, its children and grandchildren.
const discoverDepth = 2

// discoverSkipNames are directories that never contain a project worth
// reporting; dot-prefixed directories are skipped by the same rule.
var discoverSkipNames = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	".venv":        {},
}

// RepoRef identifies one initialized repository found under a root.
type RepoRef struct {
	Name string
	Path string
}

// Discover walks root (depth <= 2, never following symlinks) and returns the
// repositories that hold a doc-connector config, sorted by path.
//
// A directory with a .git entry but no config counts toward skipped instead of
// disappearing: an uninitialized checkout is a fact the caller reports.
func Discover(root string) (repos []RepoRef, skipped int, err error) {
	info, serr := os.Stat(root)
	if serr != nil {
		if os.IsNotExist(serr) {
			return nil, 0, &Error{
				Code: codeUsage,
				Msg:  fmt.Sprintf("root %s does not exist", root),
				Hint: fmt.Sprintf("check that %s exists and is a directory containing your repositories", root),
				Err:  serr,
			}
		}
		return nil, 0, &Error{
			Code: codeUsage,
			Msg:  fmt.Sprintf("cannot read root %s: %v", root, serr),
			Hint: fmt.Sprintf("check the permissions on %s", root),
			Err:  serr,
		}
	}
	if !info.IsDir() {
		return nil, 0, &Error{
			Code: codeUsage,
			Msg:  fmt.Sprintf("root %s is not a directory", root),
			Hint: fmt.Sprintf("pass the parent directory that contains your repositories, not %s", root),
		}
	}

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		switch {
		case initialized(dir):
			repos = append(repos, RepoRef{Name: filepath.Base(dir), Path: dir})
		case hasGitEntry(dir):
			skipped++
		}
		if depth >= discoverDepth {
			return
		}
		entries, rerr := os.ReadDir(dir)
		if rerr != nil {
			return // an unreadable subtree is not worth failing the whole walk
		}
		for _, entry := range entries {
			name := entry.Name()
			// Entry.IsDir is false for a symlink to a directory, so
			// symlinks are never followed here.
			if !entry.IsDir() {
				continue
			}
			if strings.HasPrefix(name, ".") {
				continue
			}
			if _, skip := discoverSkipNames[name]; skip {
				continue
			}
			walk(filepath.Join(dir, name), depth+1)
		}
	}
	walk(root, 0)

	sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })
	return repos, skipped, nil
}

// initialized reports whether dir holds the discovery marker. .git may be a
// file in a worktree, so only the config path is stat'ed.
func initialized(dir string) bool {
	p := filepath.Join(dir, ".git", "doc-connector", "config.yaml")
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// hasGitEntry reports whether dir looks like a checkout at all.
func hasGitEntry(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// RepoStatus is one repository's line in a workspace report.
type RepoStatus struct {
	Name      string
	Path      string
	ProjectID string
	DocsDir   string
	State     string
	Verdict   string
	ErrText   string
	Ahead     int
	Behind    int
	Conflicts int
	ErrCode   int
}

// WorkspaceSummary counts repositories by verdict.
type WorkspaceSummary struct {
	Repos   int
	Current int
	Ahead   int
	Behind  int
	Blocked int
	Failed  int
	Locked  int
	Skipped int
}

// WorkspaceResult is the aggregate report for one root.
type WorkspaceResult struct {
	Root    string
	Repos   []RepoStatus
	Summary WorkspaceSummary
}

// WorkspaceStatus reports every initialized repository under root.
//
// Repositories are visited serially so two status runs cannot race on the same
// fetch cache, and one failure never hides the others: its error is recorded
// and the walk continues.
func WorkspaceStatus(ctx context.Context, env Env, root string) (*WorkspaceResult, error) {
	refs, skipped, err := Discover(root)
	if err != nil {
		return nil, err
	}

	res := &WorkspaceResult{Root: root, Repos: make([]RepoStatus, 0, len(refs))}
	for _, ref := range refs {
		rs := RepoStatus{Name: ref.Name, Path: ref.Path}
		st, serr := Status(ctx, Env{Cwd: ref.Path, Stdout: io.Discard, Stderr: env.Stderr}, StatusOptions{})
		if st != nil {
			rs.ProjectID = st.ProjectID
			rs.DocsDir = st.DocsDir
			rs.State = st.Summary.String()
			rs.Ahead = st.Summary.LocalChanges
			rs.Behind = st.Summary.RemoteChanges
			rs.Conflicts = st.Summary.Conflicts
		}
		if serr != nil {
			rs.ErrText = serr.Error()
			rs.ErrCode = errorExitCode(serr)
		}
		rs.Verdict = verdictFor(rs.State, rs.Conflicts, serr)
		res.Repos = append(res.Repos, rs)
		res.Summary.count(rs.Verdict)
	}
	res.Summary.Repos = len(refs)
	res.Summary.Skipped = skipped
	return res, nil
}

// count records one verdict. Unknown verdicts are ignored rather than
// miscounted; the caller only ever produces the six documented values.
func (s *WorkspaceSummary) count(verdict string) {
	switch verdict {
	case "current":
		s.Current++
	case "ahead":
		s.Ahead++
	case "behind":
		s.Behind++
	case "blocked":
		s.Blocked++
	case "failed":
		s.Failed++
	case "locked":
		s.Locked++
	}
}

// verdictFor reduces one repository's facts to the single word a human scans.
func verdictFor(repoState string, conflicts int, err error) string {
	switch {
	case isLockError(err):
		return "locked"
	case err != nil:
		return "failed"
	case conflicts > 0 || repoState == "diverged":
		return "blocked"
	case repoState == "clean":
		return "current"
	case repoState == "ahead":
		return "ahead"
	case repoState == "behind":
		return "behind"
	default:
		// Status always sets a state when it returns no error; a blank state
		// means something went wrong that was not reported as an error.
		return "failed"
	}
}

// isLockError recognises the state layer's lock failure. The message check is
// the fallback for a lock error that lost its sentinel while being wrapped.
func isLockError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, state.ErrLocked) {
		return true
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() == codeGuard {
		return strings.Contains(strings.ToLower(err.Error()), "lock")
	}
	return false
}

// errorExitCode reads the documented exit code off an error, defaulting to the
// global fallback for an uncoded failure.
func errorExitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return codeConflict
}

// --- batch sync ------------------------------------------------------------

// Verdict vocabulary for a batch sync. "current" means nothing to do;
// "pulled"/"pushed" mean the batch changed that repository; "blocked" means a
// human decision is required; "locked"/"failed" mean the batch could not act.
const (
	SyncCurrent = "current"
	SyncPulled  = "pulled"
	SyncPushed  = "pushed"
	SyncBlocked = "blocked"
	SyncFailed  = "failed"
	SyncLocked  = "locked"
)

// SyncOptions controls one batch run.
type SyncOptions struct {
	// DryRun reports what would happen without writing anything.
	DryRun bool
	// FailFast stops at the first repository that needs attention.
	FailFast bool
}

// RepoSync is one repository's outcome in a batch sync.
type RepoSync struct {
	Name      string
	Path      string
	ProjectID string
	DocsDir   string
	State     string
	Ahead     int
	Behind    int
	Conflicts int
	Verdict   string
	Detail    string
	ErrText   string
	ErrCode   int
}

// SyncSummary counts repositories by sync verdict.
type SyncSummary struct {
	Repos   int
	Current int
	Pulled  int
	Pushed  int
	Blocked int
	Failed  int
	Locked  int
	Skipped int
}

// SyncResult is the outcome of one batch run.
type SyncResult struct {
	Root    string
	Repos   []RepoSync
	Summary SyncSummary
}

// WorkspaceSync drives every initialized repository under root, one at a time.
//
// Serial is mandatory, not a simplification: several projects may share one
// private remote, and concurrent pushes would race on its lease until the
// retry budget is exhausted. Each repository re-fetches through its own
// Status call before any decision, so the decision is made on current facts.
//
// It never resolves a conflict: a diverged repository is reported as blocked
// with its local files and conflict copies untouched.
func WorkspaceSync(ctx context.Context, env Env, root string, opts SyncOptions) (*SyncResult, error) {
	refs, skipped, err := Discover(root)
	if err != nil {
		return nil, err
	}

	res := &SyncResult{Root: root, Repos: make([]RepoSync, 0, len(refs))}
	for _, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		rs := syncOne(ctx, env, ref, opts)
		res.Repos = append(res.Repos, rs)
		res.Summary.count(rs.Verdict)
		if opts.FailFast && rs.Verdict != SyncCurrent && rs.Verdict != SyncPulled && rs.Verdict != SyncPushed {
			break
		}
	}
	res.Summary.Repos = len(res.Repos)
	res.Summary.Skipped = skipped
	return res, nil
}

// syncOne decides and acts for a single repository.
func syncOne(ctx context.Context, env Env, ref RepoRef, opts SyncOptions) RepoSync {
	rs := RepoSync{Name: ref.Name, Path: ref.Path}
	repoEnv := Env{Cwd: ref.Path, Stdout: io.Discard, Stderr: env.Stderr}

	st, serr := Status(ctx, repoEnv, StatusOptions{})
	if st != nil {
		rs.ProjectID = st.ProjectID
		rs.DocsDir = st.DocsDir
		rs.State = st.Summary.String()
		rs.Ahead = st.Summary.LocalChanges
		rs.Behind = st.Summary.RemoteChanges
		rs.Conflicts = st.Summary.Conflicts
	}
	if serr != nil {
		return failedSync(rs, serr, SyncFailed)
	}

	switch {
	case rs.Conflicts > 0 || rs.State == "diverged":
		rs.Verdict = SyncBlocked
		rs.Detail = "run status in that repository; the batch never resolves conflicts"
		return rs
	case rs.State == "clean":
		rs.Verdict = SyncCurrent
		return rs
	case rs.State == "behind":
		if opts.DryRun {
			rs.Verdict = SyncCurrent
			rs.Detail = "would pull"
			return rs
		}
		_, perr := Pull(ctx, repoEnv, PullOptions{})
		return actedSync(rs, perr, SyncPulled, "pull kept a conflict; run status in that repository")
	case rs.State == "ahead":
		if opts.DryRun {
			rs.Verdict = SyncCurrent
			rs.Detail = "would push"
			return rs
		}
		_, perr := Push(ctx, repoEnv, PushOptions{})
		return actedSync(rs, perr, SyncPushed, "push refused; run status in that repository")
	default:
		rs.Verdict = SyncFailed
		rs.Detail = "unexpected status " + rs.State
		return rs
	}
}

// actedSync maps an attempted pull/push onto a verdict. A lock is reported as
// locked, a conflict as blocked, anything else as failed — the three need
// different human actions.
func actedSync(rs RepoSync, err error, success, conflictDetail string) RepoSync {
	if err == nil {
		rs.Verdict = success
		return rs
	}
	switch {
	case isLockError(err):
		return failedSync(rs, err, SyncLocked)
	case errorExitCode(err) == codeConflict:
		rs.Verdict = SyncBlocked
		rs.Detail = conflictDetail
		rs.ErrText = err.Error()
		rs.ErrCode = codeConflict
		return rs
	default:
		return failedSync(rs, err, SyncFailed)
	}
}

func failedSync(rs RepoSync, err error, verdict string) RepoSync {
	rs.Verdict = verdict
	rs.ErrText = err.Error()
	rs.ErrCode = errorExitCode(err)
	if isLockError(err) {
		rs.Verdict = SyncLocked
	}
	return rs
}

// count records one verdict; unknown verdicts are ignored rather than
// miscounted.
func (s *SyncSummary) count(verdict string) {
	switch verdict {
	case SyncCurrent:
		s.Current++
	case SyncPulled:
		s.Pulled++
	case SyncPushed:
		s.Pushed++
	case SyncBlocked:
		s.Blocked++
	case SyncFailed:
		s.Failed++
	case SyncLocked:
		s.Locked++
	}
}
