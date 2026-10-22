package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/gitstore"
	"github.com/LZD121/doc-connector/internal/ignore"
	"github.com/LZD121/doc-connector/internal/machine"
	"github.com/LZD121/doc-connector/internal/project"
	"github.com/LZD121/doc-connector/internal/scan"
	"github.com/LZD121/doc-connector/internal/state"
)

// lockWait bounds how long a second writer waits before exiting 3.
var lockWait = 10 * time.Second

// cmdInit binds the repository to a private remote.
func cmdInit(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remote := fs.String("remote", "", "private remote URL")
	projectID := fs.String("project-id", "", "explicit 12-hex project id")
	branch := fs.String("branch", "", "remote branch")
	docsDir := fs.String("docs-dir", config.DefaultDocsDir, "docs directory")
	machineFlag := fs.String("machine", "", "machine identity")
	force := fs.Bool("force", false, "accept an explicit id that differs from the derived one")

	helped, err := parseFlags(fs, args, e.Stdout, initUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}
	explicitFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicitFlags[f.Name] = true })

	remoteURL := strings.TrimSpace(*remote)
	if remoteURL == "" {
		return Errorf(CodeUsage, "init: --remote is required").
			WithHint("create an empty private repository and pass its URL, for example git@github.com:me/private-docs.git")
	}
	if config.EmbeddedCredentials(remoteURL) {
		return Errorf(CodeUsage, "init: --remote contains embedded credentials").
			WithHint("use an SSH URL or a credential helper; doc-connector never stores secrets")
	}
	docsRel, err := normalizeDocsDir(*docsDir)
	if err != nil {
		return err
	}

	r := gitstore.New(e.Cwd)
	if err := gitstore.EnsureMinimum(ctx, r); err != nil {
		return Wrap(err, CodeUsage)
	}
	if _, err := r.Run(ctx, "check-ref-format", "--branch", branchOrDefault(*branch)); err != nil && *branch != "" {
		return Errorf(CodeUsage, "init: invalid branch name %q", *branch).
			WithHint("git check-ref-format rejected it; use a name like \"main\" or \"docs/sync\"")
	}

	repo, err := project.Discover(ctx, r, e.Cwd)
	if err != nil {
		return Wrap(err, CodeUsage)
	}
	work := gitstore.New(repo.WorkTree)

	id, err := project.Resolve(ctx, work, repo, *projectID, *force)
	if err != nil {
		return Wrap(err, CodeUsage)
	}

	// Existing configuration: never silently repoint a project at a different id.
	existing, _, cerr := config.Load(repo.GitDir)
	if cerr != nil && !errors.Is(cerr, config.ErrNotFound) {
		return Wrap(cerr, CodeUsage)
	}
	if existing != nil && existing.Project.ID != id.ID {
		return Errorf(CodeUsage, "this repository is already initialized as project %s, but the resolved id is %s",
			existing.Project.ID, id.ID).
			WithHint("restore the original history or remove .git/doc-connector/config.yaml yourself; doc-connector never repoints a project")
	}

	if err := checkDocsDir(repo.WorkTree, docsRel); err != nil {
		return Wrap(err, CodeGuard)
	}

	// Before any write: the docs directory must not be tracked.
	if err := project.GuardUntracked(ctx, work, docsRel); err != nil {
		return Wrap(err, CodeGuard)
	}

	name := machine.Hostname()
	if existing != nil && existing.Machine != "" && !explicitFlags["machine"] {
		name = existing.Machine
	}
	if explicitFlags["machine"] {
		name = machine.Sanitize(*machineFlag)
	}

	lockCtx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	lock, err := state.Acquire(lockCtx, repo.GitDir, "init", name)
	if err != nil {
		return Wrap(err, CodeGuard)
	}
	defer lock.Release()

	// Remote branch: explicit flag > existing config > advertised HEAD > main.
	branchName := *branch
	if branchName == "" && existing != nil && existing.Branch != "" {
		branchName = existing.Branch
	}
	detected, empty, derr := gitstore.DetectBranch(ctx, work, remoteURL)
	if derr != nil {
		return remoteFailure(derr)
	}
	_ = empty
	if branchName == "" {
		branchName = detected
	}
	if branchName == "" {
		branchName = config.DefaultBranch
	}

	storeDir := filepath.Join(config.Dir(repo.GitDir), "store.git")
	store := gitstore.NewStore(work, storeDir)
	if err := store.EnsureBare(ctx, branchName); err != nil {
		return Wrap(err, 1)
	}
	if err := store.Fetch(ctx, remoteURL); err != nil {
		return remoteFailure(err)
	}
	tip, err := store.RefTip(ctx, gitstore.RemoteBranch(branchName))
	if err != nil {
		return Wrap(err, 1)
	}
	treeOID, hasPrefix, err := store.TreeOf(ctx, tip, id.ID)
	if err != nil {
		return Wrap(err, 1)
	}

	// Hide every mapped docs directory, then prove the isolation invariant.
	// Re-running init on a multi-directory config must keep the other mappings:
	// dropping them from the managed block would un-hide those directories.
	docsPaths := []string{docsRel}
	if !explicitFlags["docs-dir"] && existing != nil && len(existing.DocsDirs) > 0 {
		docsPaths = docsPaths[:0]
		for _, m := range existing.DocsDirs {
			docsPaths = append(docsPaths, m.Path)
		}
	}
	excludePath := project.ExcludePath(repo.CommonDir)
	if _, err := project.EnsureExcludeDirs(excludePath, docsPaths); err != nil {
		return Wrap(err, CodeGuard)
	}
	if err := project.GuardAllDirs(ctx, work, docsPaths, excludePath); err != nil {
		return Wrap(err, CodeGuard)
	}

	// Persist only after every check passed.
	now := state.Now()
	cfg := &config.Config{
		Schema:    config.SchemaVersion,
		Remote:    remoteURL,
		Branch:    branchName,
		DocsDir:   docsRel,
		DocsDirs:  []config.DocsMapping{{Path: docsRel}},
		Machine:   name,
		Project:   config.Project{ID: id.ID, IDSource: id.Source, RootCommit: id.RootCommit, ObjectFormat: id.ObjectFormat, Adoption: "none"},
		Limits:    config.DefaultLimits(),
		CreatedAt: now,
		CreatedBy: name,
	}
	if existing != nil {
		cfg.Ignore = existing.Ignore
		cfg.Limits = existing.Limits
		cfg.Author = existing.Author
		cfg.CreatedAt = existing.CreatedAt
		cfg.CreatedBy = existing.CreatedBy
		cfg.Extra = existing.Extra
		cfg.Project.Adoption = existing.Project.Adoption
		if !explicitFlags["docs-dir"] {
			cfg.DocsDir = existing.DocsDir
			cfg.DocsDirs = existing.DocsDirs
		}
		if !explicitFlags["branch"] {
			cfg.Branch = existing.Branch
			if cfg.Branch == "" {
				cfg.Branch = branchName
			}
		}
		if !explicitFlags["remote"] {
			cfg.Remote = existing.Remote
		}
	}
	if err := cfg.Validate(); err != nil {
		return Wrap(err, CodeUsage)
	}
	if err := config.Save(repo.GitDir, cfg); err != nil {
		return Wrap(err, 1)
	}

	st, err := state.Load(repo.GitDir)
	if err != nil {
		return Wrap(err, CodeUsage)
	}
	if err := st.CheckIdentity(cfg.Project.ID, cfg.Branch); err != nil {
		return Wrap(err, CodeUsage)
	}
	st.ProjectID = cfg.Project.ID
	st.Branch = cfg.Branch
	adopted := false
	if st.SyncedCommit == "" && hasPrefix {
		// A fresh machine has no local documents yet: claiming the remote tip as
		// the baseline would make pull classify every remote file as a local
		// deletion and download nothing. Only adopt the baseline when local
		// content exists; otherwise leave it empty so pull materializes the files.
		if localHasFiles(ctx, store, repo.WorkTree, cfg.DocsDir) {
			st.SyncedCommit = tip
			st.BaselineTree = treeOID
		}
		adopted = true
	}
	if err := state.Save(repo.GitDir, st); err != nil {
		return Wrap(err, 1)
	}

	fmt.Fprintf(e.Stdout, "project: %s (%s)\n", id.ID, cfg.Project.IDSource)
	fmt.Fprintf(e.Stdout, "remote: %s (%s)\n", cfg.Remote, cfg.Branch)
	fmt.Fprintf(e.Stdout, "docs dir: %s\n", cfg.DocsDir)
	fmt.Fprintf(e.Stdout, "machine: %s\n", cfg.Machine)
	if st.SyncedCommit != "" {
		fmt.Fprintf(e.Stdout, "base: %s\n", shortOID(st.SyncedCommit))
	} else {
		fmt.Fprintln(e.Stdout, "base: none")
	}
	if adopted || (hasPrefix && st.SyncedCommit == tip) {
		fmt.Fprintln(e.Stdout, "remote prefix exists; local docs were not modified")
		fmt.Fprintln(e.Stdout, "next: doc-connector pull")
	} else {
		fmt.Fprintln(e.Stdout, "next: doc-connector push")
	}
	return nil
}

// remoteFailure maps any ls-remote/fetch failure to exit 4.
func remoteFailure(err error) error {
	return Wrap(&Error{Code: CodeRemote, Msg: err.Error(), Err: err}, CodeRemote)
}

func normalizeDocsDir(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", Errorf(CodeUsage, "init: --docs-dir must not be empty")
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	if !filepath.IsLocal(cleaned) || cleaned == "." {
		return "", Errorf(CodeUsage, "init: --docs-dir %q must be a relative directory inside the repository", p).
			WithHint("examples: private-docs, notes/private")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".git" {
			return "", Errorf(CodeUsage, "init: --docs-dir must not contain a .git component")
		}
	}
	return cleaned, nil
}

func checkDocsDir(workTree, docsRel string) error {
	full := filepath.Join(workTree, filepath.FromSlash(docsRel))
	info, err := os.Lstat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // first init does not create the directory
		}
		return &project.Error{Code: 3, Msg: fmt.Sprintf("cannot inspect docs directory %q: %v", docsRel, err), Err: err}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &project.Error{
			Code: 3,
			Msg:  fmt.Sprintf("docs directory %q is a symbolic link", docsRel),
			Hint: "point docs_dir at a real directory; doc-connector never follows the docs root symlink",
		}
	}
	if !info.IsDir() {
		return &project.Error{Code: 3, Msg: fmt.Sprintf("docs path %q is not a directory", docsRel)}
	}
	return nil
}

func localHasFiles(ctx context.Context, store *gitstore.Store, workTree, docsDir string) bool {
	abs := filepath.Join(workTree, filepath.FromSlash(docsDir))
	if info, err := os.Lstat(abs); err != nil || !info.IsDir() {
		return false
	}
	res, err := scan.Scan(ctx, store, abs, scan.Options{Filter: ignore.New(nil)})
	if err != nil {
		return false
	}
	return len(res.Tree) > 0
}

func shortOID(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}

func branchOrDefault(b string) string {
	if b == "" {
		return config.DefaultBranch
	}
	return b
}
