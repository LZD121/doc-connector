package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// ErrLocked means another process holds the project lock.
var ErrLocked = errors.New("project lock is held")

// Lock is an exclusive, cross-process project lock. The kernel releases it if
// the process dies, so there is no stale-lock guessing.
type Lock struct {
	path string
	file *flock.Flock
}

// Holder is the diagnostic record written into the lock file: who holds (or
// last held) the project lock. The lock itself is the flock; this is a note.
type Holder struct {
	Schema    int    `json:"schema"`
	PID       int    `json:"pid"`
	Machine   string `json:"machine"`
	Cmd       string `json:"cmd"`
	StartedAt string `json:"started_at"`
}

// Probe reads the lock record without taking the lock, so it is safe while
// another process works. found is false when the file is absent or empty; a
// malformed file is reported as found with the zero Holder and an error the
// caller may ignore. found=true does not prove a live holder: the file is
// deliberately left behind after Release, so a stale record also reports found.
func Probe(gitDir string) (Holder, bool, error) {
	path := filepath.Join(Dir(gitDir), "lock")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Holder{}, false, nil
		}
		return Holder{}, false, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Holder{}, false, nil
	}
	var h Holder
	if err := json.Unmarshal(data, &h); err != nil {
		return Holder{}, true, err
	}
	return h, true, nil
}

// Acquire takes the exclusive lock at <git-dir>/doc-connector/lock, polling
// every 250ms until ctx is done. Callers bound the wait (10s in the CLI).
func Acquire(ctx context.Context, gitDir, cmd, machine string) (*Lock, error) {
	if err := os.MkdirAll(Dir(gitDir), 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(Dir(gitDir), "lock")
	fl := flock.New(path)
	ok, err := fl.TryLockContext(ctx, 250*time.Millisecond)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, lockedError(path)
		}
		return nil, err
	}
	if !ok {
		return nil, lockedError(path)
	}

	info := Holder{
		Schema:    SchemaVersion,
		PID:       os.Getpid(),
		Machine:   machine,
		Cmd:       cmd,
		StartedAt: nowRFC3339(),
	}
	if data, merr := json.Marshal(info); merr == nil {
		// Diagnostic only; the lock itself is the flock.
		_ = os.WriteFile(path, data, 0o600)
	}
	return &Lock{path: path, file: fl}, nil
}

func lockedError(path string) error {
	holder := ""
	if data, err := os.ReadFile(path); err == nil {
		var info Holder
		if json.Unmarshal(data, &info) == nil && info.PID != 0 {
			holder = fmt.Sprintf(" (held by pid %d on %s, command %q)", info.PID, info.Machine, info.Cmd)
		}
	}
	return &Error{
		Code: 3,
		Msg:  fmt.Sprintf("another doc-connector process holds %s%s", path, holder),
		Hint: "wait for that process to finish; the lock is released automatically if it dies",
		Err:  ErrLocked,
	}
}

// Release unlocks. The lock file is deliberately left in place.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Unlock()
}

// Path returns the lock file path (for diagnostics).
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
