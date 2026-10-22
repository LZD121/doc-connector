package gitstore

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MinimumMajor and MinimumMinor are the floor documented in PROJECT.md and
// STACK.md. Plumbing added after 2.30 (`--path-format`, `check-ignore
// --no-index`) must not be used.
const (
	MinimumMajor = 2
	MinimumMinor = 30
)

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)`)

// ParseVersion extracts major.minor from any `git version` output, including
// Apple (`2.39.3 (Apple Git-145)`) and Windows (`2.47.1.windows.1`) suffixes.
func ParseVersion(out string) (major, minor int, raw string, err error) {
	raw = strings.TrimSpace(out)
	m := versionPattern.FindStringSubmatch(raw)
	if m == nil {
		return 0, 0, raw, fmt.Errorf("cannot parse git version from %q", raw)
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, raw, nil
}

// EnsureMinimum runs `git version` and rejects anything older than 2.30.
func EnsureMinimum(ctx context.Context, r *Runner) error {
	out, err := r.Run(ctx, "version")
	if err != nil {
		return err
	}
	major, minor, raw, perr := ParseVersion(out)
	if perr != nil {
		return &Error{
			Args: []string{"version"},
			Code: -1,
			Kind: KindUnsupportedVersion,
			Err:  perr,
		}
	}
	if major < MinimumMajor || (major == MinimumMajor && minor < MinimumMinor) {
		return &Error{
			Args: []string{"version"},
			Code: -1,
			Kind: KindUnsupportedVersion,
			Err: fmt.Errorf("git %s is too old: doc-connector requires git >= %d.%d",
				raw, MinimumMajor, MinimumMinor),
			Stderr: fmt.Sprintf("git %s is too old: doc-connector requires git >= %d.%d",
				raw, MinimumMajor, MinimumMinor),
		}
	}
	return nil
}
