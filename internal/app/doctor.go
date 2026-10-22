package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/state"
)

// Check statuses reported by Doctor.
const (
	CheckOK   = "ok"
	CheckWarn = "warn"
	CheckFail = "fail"
)

// Check is one doctor finding. ID is stable so scripts and tests can address a
// single check; Detail names concrete paths, never "somewhere in the config".
type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// DoctorSummary counts checks by status.
type DoctorSummary struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

// DoctorResult is a complete diagnostic run. RemoteUnreachable marks a failure
// that is purely "the local mirror has no remote tip", so the CLI can choose
// exit code 4 without inspecting detail strings.
type DoctorResult struct {
	Checks            []Check       `json:"checks"`
	Summary           DoctorSummary `json:"summary"`
	RemoteUnreachable bool          `json:"remote_unreachable,omitempty"`
}

// Doctor diagnoses one project without changing it: it creates no directory,
// writes no file and never takes the lock (only state.Probe reads it). Individual
// check failures are data; the returned error is reserved for context
// cancellation.
//
// Checks run in a fixed order and every one of them reports, so a single broken
// repository still yields the full picture. The only short circuit is repository
// discovery: everything after it needs the git dir, so those checks are omitted
// and the repository check says so.
func Doctor(ctx context.Context, env Env) (*DoctorResult, error) {
	res := &DoctorResult{Checks: []Check{}}
	add := func(id, status, detail, hint string) {
		res.Checks = append(res.Checks, Check{ID: id, Status: status, Detail: detail, Hint: hint})
	}
	finish := func() (*DoctorResult, error) {
		for _, c := range res.Checks {
			switch c.Status {
			case CheckOK:
				res.Summary.OK++
			case CheckWarn:
				res.Summary.Warn++
			case CheckFail:
				res.Summary.Fail++
			}
		}
		return res, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r := gitstore.New(env.Cwd)
	if err := gitstore.EnsureMinimum(ctx, r); err != nil {
		add("git-version", CheckFail, err.Error(), gitCommandHint(err))
	} else {
		detail := ""
		if out, verr := r.Run(ctx, "version"); verr == nil {
			detail = strings.TrimSpace(out)
		}
		add("git-version", CheckOK, detail, "")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	repo, err := project.Discover(ctx, r, env.Cwd)
	if err != nil {
		add("repository", CheckFail,
			err.Error()+"; repository-scoped checks skipped", hintOf(err))
		return finish()
	}
	add("repository", CheckOK, repo.WorkTree, "")
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Config and state are loaded here but reported at their fixed position:
	// the isolation checks need docs_dir and the managed exclude block.
	excludePath := project.ExcludePath(repo.CommonDir)
	cfg, cfgWarnings, cfgErr := config.Load(repo.GitDir)
	st, stErr := state.Load(repo.GitDir)
	work := gitstore.New(repo.WorkTree)

	skipped := []string{}
	if cfgErr == nil {
		checkExclude(add, excludePath, config.FilePath(repo.GitDir), cfg.DocsDir)
		checkUntracked(ctx, add, work, cfg.DocsDir)
		checkIgnored(ctx, add, work, cfg.DocsDir, excludePath)
	} else {
		skipped = append(skipped, "exclude", "untracked", "ignored", "remote")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	checkConfig(add, repo, cfg, cfgWarnings, cfgErr, st, stErr, skipped)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	checkLock(add, repo)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if cfgErr == nil {
		res.RemoteUnreachable = checkRemote(ctx, add, repo, work, cfg)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	checkArtifacts(add, repo, cfg)
	return finish()
}

// checkExclude verifies the managed block in info/exclude, matching it against
// the configured docs directory. The block is the only writable ignore source
// doc-connector accepts, so a drifted block is a hard failure.
func checkExclude(add func(id, status, detail, hint string), excludePath, cfgPath, docsDir string) {
	data, err := os.ReadFile(excludePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			add("exclude", CheckWarn, fmt.Sprintf("%s does not exist", excludePath),
				`run "doc-connector init --remote <url>" to write the managed block`)
			return
		}
		add("exclude", CheckWarn, fmt.Sprintf("cannot read %s: %v", excludePath, err),
			"check the permissions of the git common dir")
		return
	}

	content := string(data)
	begin := strings.Index(content, project.ExcludeBegin)
	if begin < 0 {
		add("exclude", CheckWarn,
			fmt.Sprintf("%s has no %q block", excludePath, project.ExcludeBegin),
			`run "doc-connector init --remote <url>" to write the managed block`)
		return
	}
	rest := content[begin:]
	end := strings.Index(rest, project.ExcludeEnd)
	if end < 0 {
		add("exclude", CheckWarn,
			fmt.Sprintf("the block starting at %s is not terminated by %q", excludePath, project.ExcludeEnd),
			fmt.Sprintf("re-run init, or add %q to %s", project.ExcludeEnd, excludePath))
		return
	}
	block := rest[:end+len(project.ExcludeEnd)]
	want := strings.TrimSpace(project.ExcludeBlock(docsDir))
	if strings.TrimSpace(block) != want {
		add("exclude", CheckFail,
			fmt.Sprintf("managed block in %s does not match the configured docs directory %q: found %q, want %q",
				excludePath, docsDir, strings.TrimSpace(block), want),
			fmt.Sprintf("restore the block in %s, or fix docs_dir in %s", excludePath, cfgPath))
		return
	}
	add("exclude", CheckOK, fmt.Sprintf("%s ignores %q exactly once", excludePath, docsDir), "")
}

// checkUntracked refuses when any path under the docs directory is tracked.
func checkUntracked(ctx context.Context, add func(id, status, detail, hint string), work *gitstore.Runner, docsDir string) {
	if err := project.GuardUntracked(ctx, work, docsDir); err != nil {
		add("untracked", CheckFail, err.Error(), hintOf(err))
		return
	}
	add("untracked", CheckOK, fmt.Sprintf("no path under %q is tracked", docsDir), "")
}

// checkIgnored requires the ignore rule to come from the managed exclude block.
func checkIgnored(ctx context.Context, add func(id, status, detail, hint string), work *gitstore.Runner, docsDir, excludePath string) {
	if err := project.GuardIgnored(ctx, work, docsDir, excludePath); err != nil {
		add("ignored", CheckFail, err.Error(), hintOf(err))
		return
	}
	add("ignored", CheckOK, fmt.Sprintf("%q is ignored by %s", docsDir, excludePath), "")
}

// checkConfig compares the human config with the machine state. The two files
// name the same project or the wrong directory gets synced.
func checkConfig(add func(id, status, detail, hint string), repo *project.Repo, cfg *config.Config, warnings []string, cfgErr error, st state.State, stErr error, skipped []string) {
	cfgPath := config.FilePath(repo.GitDir)
	statePath := state.Path(repo.GitDir, "state.json")

	switch {
	case cfgErr != nil:
		detail := fmt.Sprintf("%s: %v", cfgPath, cfgErr)
		if len(skipped) > 0 {
			detail += fmt.Sprintf("; checks that need it were skipped: %s", strings.Join(skipped, ", "))
		}
		hint := hintOf(cfgErr)
		switch {
		case errors.Is(cfgErr, config.ErrNotFound):
			hint = `run "doc-connector init --remote <url>" first`
		case hint == "":
			hint = fmt.Sprintf("fix the YAML in %s", cfgPath)
		}
		add("config", CheckFail, detail, hint)
	case stErr != nil:
		add("config", CheckFail, fmt.Sprintf("%s: %v", statePath, stErr),
			fmt.Sprintf("restore %s from a backup, or re-run init to rewrite the machine state", statePath))
	case st.ProjectID != "" && st.ProjectID != cfg.Project.ID:
		add("config", CheckFail,
			fmt.Sprintf("%s declares project %q but %s records %q", cfgPath, cfg.Project.ID, statePath, st.ProjectID),
			"one of the two files is from another project or an older id_source; restore the correct one, then run status")
	case len(warnings) > 0:
		add("config", CheckWarn, strings.Join(warnings, "; "),
			fmt.Sprintf("remove the unknown keys from %s; they are preserved but ignored", cfgPath))
	default:
		add("config", CheckOK, fmt.Sprintf("%s and %s agree on project %s", cfgPath, statePath, cfg.Project.ID), "")
	}
}

// checkLock reports a lock holder (or a stale lock record). It never takes the
// lock, so a busy project is described rather than waited for.
func checkLock(add func(id, status, detail, hint string), repo *project.Repo) {
	lockPath := state.Path(repo.GitDir, "lock")
	holder, found, err := state.Probe(repo.GitDir)
	hint := "wait for that process to finish; the kernel releases the lock automatically if it dies"
	if err != nil {
		add("lock", CheckWarn, fmt.Sprintf("%s exists but cannot be parsed: %v", lockPath, err),
			"the record is diagnostic only; delete the file once no doc-connector process is running")
		return
	}
	if !found {
		add("lock", CheckOK, "no lock holder", "")
		return
	}

	// The lock file outlives its holder on purpose: it is the diagnostic record
	// of the last writer. Only an actual flock tells us whether a process is
	// working right now, so take it for an instant and give it back.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	lock, aerr := state.Acquire(ctx, repo.GitDir, "doctor", "doctor")
	if aerr == nil {
		_ = lock.Release()
		last := fmt.Sprintf("last writer pid %d", holder.PID)
		if holder.Cmd != "" {
			last += fmt.Sprintf(" (%s)", holder.Cmd)
		}
		add("lock", CheckOK, fmt.Sprintf("%s is free; %s", lockPath, last), "")
		return
	}
	if isLockError(aerr) {
		add("lock", CheckWarn,
			fmt.Sprintf("pid %d on %s is running %q since %s (record in %s)",
				holder.PID, holder.Machine, holder.Cmd, holder.StartedAt, lockPath), hint)
		return
	}
	add("lock", CheckWarn, fmt.Sprintf("cannot test %s: %v", lockPath, aerr),
		"check the directory permissions for the doc-connector state directory")
}

// checkRemote reports the mirror tip without touching the network: Fetch is
// deliberately absent so doctor is safe to run offline. It returns true when the
// remote tip is simply unavailable locally, which the CLI maps to exit code 4.
func checkRemote(ctx context.Context, add func(id, status, detail, hint string), repo *project.Repo, work *gitstore.Runner, cfg *config.Config) bool {
	if cfg.Remote == "" {
		add("remote", CheckFail, fmt.Sprintf("%s has no remote url", config.FilePath(repo.GitDir)),
			`run "doc-connector init --remote <url>"`)
		return false
	}

	store := gitstore.NewStore(work, filepath.Join(config.Dir(repo.GitDir), "store.git"))
	ref := gitstore.RemoteBranch(cfg.Branch)
	base := fmt.Sprintf("remote %s branch %s; %s", cfg.Remote, cfg.Branch, fetchedLabel(repo.GitDir))

	tip, err := store.RefTip(ctx, ref)
	if err != nil {
		add("remote", CheckFail, fmt.Sprintf("%s; cannot read the local mirror: %v", base, err),
			`run "doc-connector pull" to rebuild the local mirror`)
		return true
	}
	if tip == "" {
		// An empty remote legitimately has no tip yet: that is not a network
		// failure, and doctor never uses the network to prove either way.
		add("remote", CheckWarn, fmt.Sprintf("%s; the local mirror has no %s yet (nothing pushed, or never fetched)", base, ref),
			`run "doc-connector status" once while online to fetch the remote tip`)
		return false
	}
	add("remote", CheckOK, fmt.Sprintf("%s; mirror tip %s", base, shortOID(tip)), "")
	return false
}

// fetchedLabel renders the last fetched commit and its age, or "never fetched".
func fetchedLabel(gitDir string) string {
	f := state.LoadFetched(gitDir)
	if f.Commit == "" {
		return "never fetched"
	}
	label := "last fetched " + shortOID(f.Commit)
	if age := FetchedAge(f); age != "" {
		label += " (" + age + ")"
	}
	return label
}

// checkArtifacts lists leftovers from an interrupted run: the scratch directory,
// a journal, and conflict copies in the docs directory.
func checkArtifacts(add func(id, status, detail, hint string), repo *project.Repo, cfg *config.Config) {
	var leftovers []string
	tmp := state.TmpDir(repo.GitDir)
	if entries, err := os.ReadDir(tmp); err == nil {
		for _, e := range entries {
			leftovers = append(leftovers, filepath.Join(tmp, e.Name()))
		}
	}
	journal := state.Path(repo.GitDir, "journal.json")
	if _, err := os.Stat(journal); err == nil {
		leftovers = append(leftovers, journal)
	}
	if cfg != nil {
		docs := filepath.Join(repo.WorkTree, filepath.FromSlash(cfg.DocsDir))
		if info, err := os.Stat(docs); err == nil && info.IsDir() {
			leftovers = append(leftovers, conflictLeftovers(docs)...)
		}
	}

	if len(leftovers) == 0 {
		add("artifacts", CheckOK, "no leftover journal, scratch or conflict files", "")
		return
	}
	sort.Strings(leftovers)
	shown := leftovers
	suffix := ""
	if len(shown) > 5 {
		shown = shown[:5]
		suffix = fmt.Sprintf(" (and %d more)", len(leftovers)-5)
	}
	add("artifacts", CheckWarn,
		fmt.Sprintf("%d leftover path(s): %s%s", len(leftovers), strings.Join(shown, ", "), suffix),
		"journal.json is recovered by the next pull or push; delete the other paths once they are no longer needed")
}

// conflictLeftovers finds <name>.theirs.<machine> copies and the fallback
// .doc-connector-conflicts directory. The walk stops after six hits: the report
// caps at five paths anyway.
func conflictLeftovers(docs string) []string {
	var out []string
	_ = filepath.WalkDir(docs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		fallback := strings.Contains(filepath.ToSlash(p), "/.doc-connector-conflicts/")
		if !fallback && !strings.Contains(d.Name(), ".theirs.") {
			return nil
		}
		out = append(out, p)
		if len(out) >= 6 {
			return fs.SkipAll
		}
		return nil
	})
	return out
}

// gitCommandHint names the git command behind a version failure, plus its own
// hint when it has one.
func gitCommandHint(err error) string {
	var ge *gitstore.Error
	if !errors.As(err, &ge) || len(ge.Args) == 0 {
		return ""
	}
	hint := "git " + strings.Join(ge.Args, " ")
	if extra := ge.HintText(); extra != "" {
		hint += "; " + extra
	}
	return hint
}

// hintOf extracts the actionable hint from a coded error, if any.
func hintOf(err error) string {
	if h, ok := err.(interface{ HintText() string }); ok {
		return h.HintText()
	}
	return ""
}
