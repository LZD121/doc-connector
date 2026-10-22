package project

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/LZD121/doc-connector/internal/gitstore"
)

// Identity is the outcome of identity resolution.
type Identity struct {
	ID           string // 12 lowercase hex
	Source       string // "derived" or "explicit"
	RootCommit   string // lowercase hex; empty when explicitly supplied on an unborn/shallow repo
	ObjectFormat string
}

var (
	explicitIDPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)
	hex40Pattern      = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	hex64Pattern      = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

// DeriveID implements the frozen rule: lower(root_hex)[:12]. It never hashes,
// salts or re-encodes; the same history must yield the same directory forever.
func DeriveID(rootHex string) (string, error) {
	h := strings.TrimSpace(rootHex)
	if !hex40Pattern.MatchString(h) && !hex64Pattern.MatchString(h) {
		return "", fmt.Errorf("root commit %q is not 40 or 64 hex characters", h)
	}
	return strings.ToLower(h)[:12], nil
}

// RootCommits returns every root commit reachable from HEAD. An unborn HEAD
// returns an empty slice, not an error.
func RootCommits(ctx context.Context, r *gitstore.Runner, repo *Repo) ([]string, error) {
	local := runnerFor(r, repo)
	out, err := local.Run(ctx, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		// Distinguish "no commits yet" from a real failure.
		if _, verifyErr := local.Run(ctx, "rev-parse", "--verify", "--quiet", "HEAD"); verifyErr != nil {
			return nil, nil
		}
		return nil, err
	}
	var roots []string
	for _, line := range strings.Split(out, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			roots = append(roots, v)
		}
	}
	return roots, nil
}

// Derive resolves the identity from the repository's root commit.
func Derive(ctx context.Context, r *gitstore.Runner, repo *Repo) (Identity, error) {
	format := repo.ObjectFormat
	if format == "" {
		format = "sha1"
	}

	roots, err := RootCommits(ctx, r, repo)
	if err != nil {
		return Identity{}, err
	}

	if repo.Shallow {
		return Identity{}, usageErr("cannot derive project id: repository is a shallow clone (root commit unavailable)").
			withHint("clone with full history, or pass --project-id <12hex> explicitly")
	}
	switch len(roots) {
	case 0:
		return Identity{}, usageErr("cannot derive project id: repository has no commits (no root commit)").
			withHint("make a first commit, or pass --project-id <12hex> explicitly")
	case 1:
		// expected
	default:
		return Identity{}, usageErr("cannot derive project id: repository has %d root commits (%s)",
			len(roots), strings.Join(roots, ", ")).
			withHint("multiple roots are ambiguous; pass --project-id <12hex> explicitly")
	}

	id, err := DeriveID(roots[0])
	if err != nil {
		return Identity{}, usageErr("cannot derive project id: %v", err)
	}
	return Identity{
		ID:           id,
		Source:       "derived",
		RootCommit:   strings.ToLower(roots[0]),
		ObjectFormat: format,
	}, nil
}

// Resolve combines derivation with an optional explicit --project-id.
//
// An explicit id that disagrees with the derived one requires force: silently
// accepting it would sync two different projects into one remote directory.
func Resolve(ctx context.Context, r *gitstore.Runner, repo *Repo, explicit string, force bool) (Identity, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" && !explicitIDPattern.MatchString(explicit) {
		return Identity{}, usageErr("invalid --project-id %q: expected 12 lowercase hex characters", explicit).
			withHint("project id is lower(root commit hex)[:12], for example 743c420a8e7b")
	}

	derived, deriveErr := Derive(ctx, r, repo)

	if explicit == "" {
		if deriveErr != nil {
			return Identity{}, deriveErr
		}
		return derived, nil
	}

	format := repo.ObjectFormat
	if format == "" {
		format = "sha1"
	}
	id := Identity{ID: explicit, Source: "explicit", ObjectFormat: format}

	if deriveErr == nil {
		id.RootCommit = derived.RootCommit
		if explicit != derived.ID && !force {
			return Identity{}, usageErr("--project-id %s does not match the id derived from the root commit (%s)",
				explicit, derived.ID).
				withHint("re-run with the matching id, or pass --force if the remote directory already uses %s", explicit)
		}
		return id, nil
	}

	// Derivation impossible (shallow, unborn, multiple roots): an explicit id is
	// the documented escape hatch, so it is accepted without --force.
	return id, nil
}

func runnerFor(r *gitstore.Runner, repo *Repo) *gitstore.Runner {
	local := *r
	if repo != nil && repo.WorkTree != "" {
		local.Dir = repo.WorkTree
	}
	return &local
}
