// Package conflict names and locates <path>.theirs.<machine> artifacts.
package conflict

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"github.com/LZD121/doc-connector/internal/fsx"
)

// MaxSuffix bounds .2 ... .99; a 100th simultaneous conflict is unreasonable.
const MaxSuffix = 99

// ArtifactPath returns a free artifact path for rel. Candidates are
// <rel>.theirs.<machine>, then .2, .3 ...; an over-long candidate falls back to
// .doc-connector-conflicts/<sha1(rel)[:8]>-<base>.theirs.<machine>.
func ArtifactPath(root *fsx.Root, rel, machine string, budget int) (string, error) {
	primary := rel + ".theirs." + machine
	if fits(primary, budget) {
		if p, ok := free(root, primary); ok {
			return p, nil
		}
	}

	base := path.Base(rel)
	sum := sha1.Sum([]byte(rel))
	short := hex.EncodeToString(sum[:])[:8]
	candidate := path.Join(".doc-connector-conflicts", short+"-"+base+".theirs."+machine)
	if fits(candidate, budget) {
		if p, ok := free(root, candidate); ok {
			return p, nil
		}
	}
	return "", &fsx.Error{
		Msg:  fmt.Sprintf("cannot find a free conflict artifact name for %q", rel),
		Hint: "too many conflicts or the path budget is too small",
	}
}

func fits(p string, budget int) bool {
	return budget <= 0 || len(p) <= budget
}

func free(root *fsx.Root, candidate string) (string, bool) {
	if _, err := root.Lstat(candidate); err != nil {
		return candidate, true
	}
	for i := 2; i <= MaxSuffix; i++ {
		alt := fmt.Sprintf("%s.%d", candidate, i)
		if _, err := root.Lstat(alt); err != nil {
			return alt, true
		}
	}
	return "", false
}

// Kind maps a classification name to the stored conflict kind.
func Kind(class string) string {
	switch class {
	case "add/add":
		return "add-add"
	case "both-modified":
		return "both-modified"
	case "delete/modify":
		return "delete-modify"
	case "modify/delete":
		return "modify-delete"
	default:
		return strings.ReplaceAll(class, "/", "-")
	}
}
