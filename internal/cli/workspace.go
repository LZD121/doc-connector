package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/LZD121/doc-connector/internal/app"
)

// cmdStatusRoot reports every initialized repository under root. It is the
// aggregate half of "status": discovery only, no state is written.
func cmdStatusRoot(ctx context.Context, e *runnerEnv, root string, jsonOut bool) error {
	res, err := app.WorkspaceStatus(ctx, app.Env{Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr}, root)
	if err != nil {
		return Wrap(err, CodeUsage)
	}
	failure := workspaceFailure(res)

	if jsonOut {
		if err := e.emit(workspaceDocument(res, failure)); err != nil {
			return err
		}
		return failure
	}

	fmt.Fprintf(e.Stdout, "workspace: %s (%d repos, %d skipped)\n", res.Root, res.Summary.Repos, res.Summary.Skipped)
	for _, repo := range res.Repos {
		fmt.Fprintln(e.Stdout, repoLine(repo))
		if repo.ErrText != "" {
			fmt.Fprintf(e.Stdout, "  %s\n", repo.ErrText)
		}
	}
	fmt.Fprintf(e.Stdout, "summary: current=%d ahead=%d behind=%d blocked=%d failed=%d locked=%d\n",
		res.Summary.Current, res.Summary.Ahead, res.Summary.Behind,
		res.Summary.Blocked, res.Summary.Failed, res.Summary.Locked)
	return failure
}

// repoLine renders one repository: identity, state, counts, verdict.
func repoLine(repo app.RepoStatus) string {
	project := repo.ProjectID
	if project == "" {
		project = "-"
	}
	state := repo.State
	if state == "" {
		state = "-"
	}
	return fmt.Sprintf("%s  %s  %s  a%d/b%d  c%d  %s",
		repo.Name, project, state, repo.Ahead, repo.Behind, repo.Conflicts, repo.Verdict)
}

// workspaceFailure turns the worst per-repository code into the command's exit
// code. Repository failures are data, so this is the only error the aggregate
// path returns once discovery succeeded.
func workspaceFailure(res *app.WorkspaceResult) error {
	worst := 0
	for _, repo := range res.Repos {
		// 130 belongs to an interrupted process, which Run reports itself.
		if repo.ErrCode == CodeInterrupted || repo.ErrCode > CodeRemote {
			continue
		}
		if repo.ErrCode > worst {
			worst = repo.ErrCode
		}
	}
	if worst == 0 {
		return nil
	}
	need := 0
	for _, repo := range res.Repos {
		switch repo.Verdict {
		case "blocked", "failed", "locked":
			need++
		}
	}
	return Errorf(worst, "%d of %d repositories need attention", need, len(res.Repos)).
		WithHint("fix the repositories named above and rerun \"doc-connector status --root\"")
}

// --- JSON -----------------------------------------------------------------

type workspaceRepoDoc struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	ProjectID string `json:"project_id"`
	DocsDir   string `json:"docs_dir"`
	State     string `json:"state"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Conflicts int    `json:"conflicts"`
	Verdict   string `json:"verdict"`
	Error     string `json:"error,omitempty"`
	ErrorCode int    `json:"error_code"`
}

type workspaceSummaryDoc struct {
	Repos   int `json:"repos"`
	Current int `json:"current"`
	Ahead   int `json:"ahead"`
	Behind  int `json:"behind"`
	Blocked int `json:"blocked"`
	Failed  int `json:"failed"`
	Locked  int `json:"locked"`
	Skipped int `json:"skipped"`
}

type statusRootDoc struct {
	Envelope
	Root    string              `json:"root"`
	Repos   []workspaceRepoDoc  `json:"repos"`
	Summary workspaceSummaryDoc `json:"summary"`
}

// workspaceDocument renders one aggregate report. err is non-nil when at least
// one repository failed, so the envelope records the exit code the process
// will use while the per-repository detail stays in repos[].
func workspaceDocument(res *app.WorkspaceResult, err error) statusRootDoc {
	doc := statusRootDoc{
		Envelope: envelope("status", err),
		Root:     res.Root,
		Repos:    make([]workspaceRepoDoc, 0, len(res.Repos)),
		Summary: workspaceSummaryDoc{
			Repos:   res.Summary.Repos,
			Current: res.Summary.Current,
			Ahead:   res.Summary.Ahead,
			Behind:  res.Summary.Behind,
			Blocked: res.Summary.Blocked,
			Failed:  res.Summary.Failed,
			Locked:  res.Summary.Locked,
			Skipped: res.Summary.Skipped,
		},
	}
	for _, repo := range res.Repos {
		doc.Repos = append(doc.Repos, workspaceRepoDoc{
			Name:      repo.Name,
			Path:      repo.Path,
			ProjectID: repo.ProjectID,
			DocsDir:   repo.DocsDir,
			State:     repo.State,
			Ahead:     repo.Ahead,
			Behind:    repo.Behind,
			Conflicts: repo.Conflicts,
			Verdict:   repo.Verdict,
			Error:     repo.ErrText,
			ErrorCode: repo.ErrCode,
		})
	}
	return doc
}

// --- sync -----------------------------------------------------------------

const syncUsage = `Usage:
  doc-connector sync --root <dir> [flags]

Drives every initialized repository under <dir>: clean ones are skipped,
behind ones are pulled, ahead ones are pushed. A diverged repository or one
with unresolved conflicts is reported as blocked and never resolved for you.
Repositories are visited one at a time, each re-fetching first.

Flags:
      --root <dir>   Directory that contains the repositories (required)
      --dry-run      Report what would happen; write nothing
      --fail-fast    Stop at the first repository that needs attention
      --json         Print one JSON document
  -h, --help         Show this help
`

// cmdSync parses the flags of the aggregate sync command.
func cmdSync(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "directory that contains the repositories")
	dryRun := fs.Bool("dry-run", false, "report what would happen without writing")
	failFast := fs.Bool("fail-fast", false, "stop at the first repository that needs attention")
	jsonOut := fs.Bool("json", false, "print one JSON document")

	helped, err := parseFlags(fs, args, e.Stdout, syncUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}
	if *root == "" {
		return Errorf(CodeUsage, "sync: --root <dir> is required").
			WithHint("pass the directory that contains your repositories, for example --root ~/repos")
	}
	return cmdSyncRoot(ctx, e, *root, *dryRun, *failFast, *jsonOut)
}

func cmdSyncRoot(ctx context.Context, e *runnerEnv, root string, dryRun, failFast, jsonOut bool) error {
	res, err := app.WorkspaceSync(ctx, app.Env{Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr}, root,
		app.SyncOptions{DryRun: dryRun, FailFast: failFast})
	if err != nil {
		return Wrap(err, CodeUsage)
	}
	failure := syncFailure(res)

	if jsonOut {
		if emitErr := e.emit(syncDocument(res, failure)); emitErr != nil {
			return emitErr
		}
		return failure
	}

	mode := ""
	if dryRun {
		mode = ", dry-run"
	}
	fmt.Fprintf(e.Stdout, "sync: %s (%d repos, %d skipped%s)\n", res.Root, res.Summary.Repos, res.Summary.Skipped, mode)
	for _, repo := range res.Repos {
		fmt.Fprintf(e.Stdout, "%s\n", syncLine(repo))
		if repo.ErrText != "" {
			fmt.Fprintf(e.Stdout, "    %s\n", repo.ErrText)
		}
	}
	fmt.Fprintf(e.Stdout, "summary: current=%d pulled=%d pushed=%d blocked=%d failed=%d locked=%d\n",
		res.Summary.Current, res.Summary.Pulled, res.Summary.Pushed,
		res.Summary.Blocked, res.Summary.Failed, res.Summary.Locked)
	return failure
}

// syncLine renders one repository: identity, counts, verdict and detail.
func syncLine(repo app.RepoSync) string {
	project := repo.ProjectID
	if project == "" {
		project = "-"
	}
	state := repo.State
	if state == "" {
		state = "-"
	}
	line := fmt.Sprintf("%s  %s  %s  a%d/b%d  c%d  %s",
		repo.Name, project, state, repo.Ahead, repo.Behind, repo.Conflicts, repo.Verdict)
	if repo.Detail != "" {
		line += " — " + repo.Detail
	}
	return line
}

// syncFailure turns the worst per-repository code into the command's exit code.
func syncFailure(res *app.SyncResult) error {
	worst := 0
	need := 0
	for _, repo := range res.Repos {
		switch repo.Verdict {
		case app.SyncBlocked, app.SyncFailed, app.SyncLocked:
			need++
		}
		// 130 belongs to an interrupted process, which Run reports itself.
		if repo.ErrCode == CodeInterrupted || repo.ErrCode > CodeRemote {
			continue
		}
		if repo.ErrCode > worst {
			worst = repo.ErrCode
		}
	}
	if worst == 0 && need == 0 {
		return nil
	}
	if worst == 0 {
		worst = CodeConflict
	}
	return Errorf(worst, "%d of %d repositories need attention", need, len(res.Repos)).
		WithHint("run \"doc-connector status --root <dir>\" for the per-repository detail")
}

type syncRepoDoc struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	ProjectID string `json:"project_id"`
	DocsDir   string `json:"docs_dir"`
	State     string `json:"state"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Conflicts int    `json:"conflicts"`
	Verdict   string `json:"verdict"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorCode int    `json:"error_code"`
}

type syncSummaryDoc struct {
	Repos   int  `json:"repos"`
	Current int  `json:"current"`
	Pulled  int  `json:"pulled"`
	Pushed  int  `json:"pushed"`
	Blocked int  `json:"blocked"`
	Failed  int  `json:"failed"`
	Locked  int  `json:"locked"`
	Skipped int  `json:"skipped"`
	DryRun  bool `json:"dry_run"`
}

type syncDoc struct {
	Envelope
	Root    string         `json:"root"`
	Repos   []syncRepoDoc  `json:"repos"`
	Summary syncSummaryDoc `json:"summary"`
}

func syncDocument(res *app.SyncResult, err error) syncDoc {
	doc := syncDoc{
		Envelope: envelope("sync", err),
		Root:     res.Root,
		Repos:    make([]syncRepoDoc, 0, len(res.Repos)),
		Summary: syncSummaryDoc{
			Repos:   res.Summary.Repos,
			Current: res.Summary.Current,
			Pulled:  res.Summary.Pulled,
			Pushed:  res.Summary.Pushed,
			Blocked: res.Summary.Blocked,
			Failed:  res.Summary.Failed,
			Locked:  res.Summary.Locked,
			Skipped: res.Summary.Skipped,
		},
	}
	for _, repo := range res.Repos {
		doc.Repos = append(doc.Repos, syncRepoDoc{
			Name:      repo.Name,
			Path:      repo.Path,
			ProjectID: repo.ProjectID,
			DocsDir:   repo.DocsDir,
			State:     repo.State,
			Ahead:     repo.Ahead,
			Behind:    repo.Behind,
			Conflicts: repo.Conflicts,
			Verdict:   repo.Verdict,
			Detail:    repo.Detail,
			Error:     repo.ErrText,
			ErrorCode: repo.ErrCode,
		})
	}
	return doc
}
