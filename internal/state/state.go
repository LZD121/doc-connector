// Package state owns the hidden machine state: the last synced commit, the
// unresolved conflict set, the fetched-tip cache, the apply journal and the
// cross-process lock.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the only state schema this build understands.
const SchemaVersion = 1

// Conflict is one unresolved path after a pull.
type Conflict struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Theirs    string `json:"theirs,omitempty"`
	RemoteOID string `json:"remote_oid,omitempty"`
	BaseOID   string `json:"base_oid,omitempty"`
	At        string `json:"at"`
}

// State is the decision-relevant machine state. It is written only at success
// points; fetch timestamps live in Fetched instead.
type State struct {
	Schema       int        `json:"schema"`
	ProjectID    string     `json:"project_id"`
	Branch       string     `json:"branch"`
	SyncedCommit string     `json:"synced_commit,omitempty"`
	BaselineTree string     `json:"baseline_tree,omitempty"`
	LastSyncAt   string     `json:"last_sync_at,omitempty"`
	LastSyncKind string     `json:"last_sync_kind,omitempty"`
	Unresolved   []Conflict `json:"unresolved,omitempty"`
}

// Fetched is a pure cache of the last successful fetch. Deleting it is safe.
type Fetched struct {
	Schema int    `json:"schema"`
	At     string `json:"at"`
	Commit string `json:"commit,omitempty"`
	By     string `json:"by,omitempty"`
}

// JournalOp is one filesystem action recorded before it is applied.
type JournalOp struct {
	Action string `json:"action"` // write | delete | conflict
	Path   string `json:"path"`
	Mode   string `json:"mode,omitempty"`
	OID    string `json:"oid,omitempty"`
	Theirs string `json:"theirs,omitempty"`
	Staged string `json:"staged,omitempty"`
}

// Journal makes a partially applied pull forward-recoverable.
type Journal struct {
	Schema       int         `json:"schema"`
	Op           string      `json:"op"`
	StartedAt    string      `json:"started_at"`
	ProjectID    string      `json:"project_id"`
	RemoteCommit string      `json:"remote_commit,omitempty"`
	Ops          []JournalOp `json:"ops"`
	StateNext    string      `json:"state_next,omitempty"`
	TmpDir       string      `json:"tmp_dir,omitempty"`
}

// Dir returns <git-dir>/doc-connector.
func Dir(gitDir string) string { return filepath.Join(gitDir, "doc-connector") }

// Path returns a file inside the hidden state directory.
func Path(gitDir, name string) string { return filepath.Join(Dir(gitDir), name) }

// TmpDir returns the scratch directory used for indexes and staged blobs.
func TmpDir(gitDir string) string { return filepath.Join(Dir(gitDir), "tmp") }

// Load reads state.json; a missing file returns an empty state with the current
// schema so first-run code paths do not special-case nil.
func Load(gitDir string) (State, error) {
	st := State{Schema: SchemaVersion}
	err := ReadJSON(Path(gitDir, "state.json"), &st)
	if err != nil {
		if IsNotExist(err) {
			return st, nil
		}
		return State{}, err
	}
	if st.Schema != SchemaVersion {
		return State{}, fmt.Errorf("unsupported state schema %d (this build expects %d)", st.Schema, SchemaVersion)
	}
	return st, nil
}

// Save writes state.json atomically.
func Save(gitDir string, st State) error {
	st.Schema = SchemaVersion
	if err := os.MkdirAll(Dir(gitDir), 0o700); err != nil {
		return err
	}
	return WriteJSON(Path(gitDir, "state.json"), st)
}

// LoadFetched reads the fetch cache. A missing or unreadable cache is not an
// error: it is pure acceleration.
func LoadFetched(gitDir string) Fetched {
	var f Fetched
	_ = ReadJSON(Path(gitDir, "fetched.json"), &f)
	return f
}

// SaveFetched writes the fetch cache atomically.
func SaveFetched(gitDir string, f Fetched) error {
	f.Schema = SchemaVersion
	if err := os.MkdirAll(Dir(gitDir), 0o700); err != nil {
		return err
	}
	return WriteJSON(Path(gitDir, "fetched.json"), f)
}

// NewFetched stamps a cache entry for the current time.
func NewFetched(commit, by string) Fetched {
	return Fetched{Schema: SchemaVersion, At: nowRFC3339(), Commit: commit, By: by}
}

// LoadJournal reads journal.json. A missing journal returns (nil, nil).
func LoadJournal(gitDir string) (*Journal, error) {
	var j Journal
	err := ReadJSON(Path(gitDir, "journal.json"), &j)
	if err != nil {
		if IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}

// SaveJournal records an in-flight apply.
func SaveJournal(gitDir string, j *Journal) error {
	if j.Schema == 0 {
		j.Schema = SchemaVersion
	}
	return WriteJSON(Path(gitDir, "journal.json"), j)
}

// RemoveJournal deletes the journal after apply completes.
func RemoveJournal(gitDir string) error {
	err := os.Remove(Path(gitDir, "journal.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Error is a state-layer failure carrying an exit code.
type Error struct {
	Code int
	Msg  string
	Hint string
	Err  error
}

func (e *Error) Error() string    { return e.Msg }
func (e *Error) Unwrap() error    { return e.Err }
func (e *Error) ExitCode() int    { return e.Code }
func (e *Error) HintText() string { return e.Hint }

// CheckIdentity refuses to operate when state and config describe different
// projects or branches. Cross-wiring them would sync the wrong directory.
func (s State) CheckIdentity(projectID, branch string) error {
	if s.ProjectID == "" {
		return nil // first run
	}
	if s.ProjectID != projectID {
		return &Error{
			Code: 2,
			Msg:  fmt.Sprintf("state project_id %s does not match config project.id %s", s.ProjectID, projectID),
			Hint: "re-run init, or restore the matching config/state backup; doc-connector never syncs mismatched identities",
		}
	}
	if s.Branch != "" && branch != "" && s.Branch != branch {
		return &Error{
			Code: 2,
			Msg:  fmt.Sprintf("state branch %s does not match config branch %s", s.Branch, branch),
			Hint: "re-run init against the intended branch",
		}
	}
	return nil
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// Now stamps state updates.
func Now() string { return nowRFC3339() }
