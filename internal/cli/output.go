package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/LZD121/doc-connector/internal/app"
	"github.com/LZD121/doc-connector/internal/config"
	"github.com/LZD121/doc-connector/internal/trees"
)

// outputSchemaVersion stamps every JSON document. Consumers switch on it, so
// it changes only when a field is removed or its meaning changes.
const outputSchemaVersion = 1

// wireError is the failure object inside a document.
type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Envelope heads every JSON document. Payload types embed it, so the envelope
// fields are always encoded first.
type Envelope struct {
	SchemaVersion int        `json:"schema_version"`
	Command       string     `json:"command"`
	OK            bool       `json:"ok"`
	Error         *wireError `json:"error,omitempty"`
}

// envelope builds the document head for one command outcome.
func envelope(command string, err error) Envelope {
	env := Envelope{SchemaVersion: outputSchemaVersion, Command: command, OK: err == nil}
	if err == nil {
		return env
	}
	ce := Wrap(err, CodeConflict)
	var e *Error
	if errors.As(ce, &e) {
		env.Error = &wireError{Code: codeName(e.Code), Message: e.Msg, Hint: e.Hint}
		return env
	}
	env.Error = &wireError{Code: codeName(CodeConflict), Message: err.Error()}
	return env
}

// codeName maps the closed exit-code set onto the documented string codes.
func codeName(code int) string {
	switch code {
	case CodeOK:
		return "ok"
	case CodeConflict:
		return "conflict"
	case CodeUsage:
		return "usage"
	case CodeGuard:
		return "precondition"
	case CodeRemote:
		return "remote"
	case CodeInterrupted:
		return "interrupted"
	default:
		return "internal"
	}
}

// writeJSON writes exactly one document, with its trailing newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

// jsonRequested answers "did the user pass --json?" from raw argv, so a failure
// that happens before flag parsing can still produce a document.
// `--json=false` is not a request.
func jsonRequested(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--json" || a == "-json":
			return true
		case strings.HasPrefix(a, "--json="):
			return jsonValue(a[len("--json="):])
		case strings.HasPrefix(a, "-json="):
			return jsonValue(a[len("-json="):])
		}
	}
	return false
}

func jsonValue(v string) bool { return v != "false" && v != "0" }

// emit writes one document and records that stdout is no longer empty, so the
// fallback in Run never writes a second one.
func (e *runnerEnv) emit(v any) error {
	if err := writeJSON(e.Stdout, v); err != nil {
		return Errorf(CodeConflict, "%v", err)
	}
	e.wroteDoc = true
	return nil
}

// writeErrorDoc is the fallback for a run that failed before emitting anything.
func writeErrorDoc(w io.Writer, command string, err error) {
	_ = writeJSON(w, envelope(command, err))
}

// commandName is the verb of this invocation, for the document head.
func commandName(args []string) string {
	if len(args) == 0 {
		return ""
	}
	if strings.HasPrefix(args[0], "-") {
		return ""
	}
	return args[0]
}

// --- status ---------------------------------------------------------------

type statusProject struct {
	ID       string          `json:"id"`
	DocsDir  string          `json:"docs_dir"`
	DocsDirs []statusDocsDir `json:"docs_dirs"`
}

// statusDocsDir mirrors one configured mapping: the local directory and the
// remote prefix it syncs to ("" is the project root).
type statusDocsDir struct {
	Path   string `json:"path"`
	Prefix string `json:"prefix"`
}

type statusRemote struct {
	URL               string `json:"url"`
	Branch            string `json:"branch"`
	Tip               string `json:"tip,omitempty"`
	Checked           bool   `json:"checked"`
	FetchedAt         string `json:"fetched_at,omitempty"`
	FetchedAgeSeconds int64  `json:"fetched_age_seconds,omitempty"`
	FetchError        string `json:"fetch_error,omitempty"`
}

type statusBase struct {
	Commit string `json:"commit,omitempty"`
	Tree   string `json:"tree,omitempty"`
}

type statusCounts struct {
	Ahead     int `json:"ahead"`
	Behind    int `json:"behind"`
	Conflicts int `json:"conflicts"`
}

type statusFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	LocalOID  string `json:"local_oid,omitempty"`
	BaseOID   string `json:"base_oid,omitempty"`
	RemoteOID string `json:"remote_oid,omitempty"`
}

type statusDoc struct {
	Envelope
	Project statusProject `json:"project"`
	Remote  statusRemote  `json:"remote"`
	Base    statusBase    `json:"base"`
	State   string        `json:"state"`
	Counts  statusCounts  `json:"counts"`
	Files   []statusFile  `json:"files"`
	Ignored int           `json:"ignored"`
	Skipped []string      `json:"skipped,omitempty"`
}

// statusDocument renders one status report. err is non-nil when the remote
// could not be checked: the payload is still emitted, with the error attached.
func statusDocument(res *app.StatusResult, err error) statusDoc {
	doc := statusDoc{
		Envelope: envelope("status", err),
		Project:  statusProject{ID: res.ProjectID, DocsDir: res.DocsDir, DocsDirs: statusDocsDirs(res.DocsDirs)},
		Remote: statusRemote{
			URL:               res.Remote,
			Branch:            res.Branch,
			Tip:               res.RemoteTip,
			Checked:           res.RemoteChecked,
			FetchedAt:         res.Fetched.At,
			FetchedAgeSeconds: res.FetchedAgeSeconds,
			FetchError:        fetchErrorText(res.FetchError),
		},
		Base:    statusBase{Commit: res.BaseCommit, Tree: res.BaseTree},
		State:   res.Summary.String(),
		Counts:  statusCounts{Ahead: res.Summary.LocalChanges, Behind: res.Summary.RemoteChanges, Conflicts: res.Summary.Conflicts},
		Files:   statusFiles(res.Actions),
		Ignored: res.Ignored,
		Skipped: res.Skipped,
	}
	if doc.Files == nil {
		doc.Files = []statusFile{}
	}
	return doc
}

func statusDocsDirs(mappings []config.DocsMapping) []statusDocsDir {
	dirs := make([]statusDocsDir, 0, len(mappings))
	for _, m := range mappings {
		dirs = append(dirs, statusDocsDir{Path: m.Path, Prefix: m.Prefix})
	}
	return dirs
}

func statusFiles(actions []trees.Action) []statusFile {
	files := make([]statusFile, 0, len(actions))
	for _, a := range actions {
		if a.Status == "" || a.Status == "=" {
			continue
		}
		f := statusFile{Path: a.Path, Status: a.Status}
		if a.Local != nil {
			f.LocalOID = a.Local.OID
		}
		if a.Base != nil {
			f.BaseOID = a.Base.OID
		}
		if a.Remote != nil {
			f.RemoteOID = a.Remote.OID
		}
		files = append(files, f)
	}
	return files
}

func fetchErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// --- push -----------------------------------------------------------------

type pushDoc struct {
	Envelope
	DryRun   bool     `json:"dry_run"`
	NoOp     bool     `json:"no_op"`
	Tracked  int      `json:"tracked"`
	Added    int      `json:"added"`
	Modified int      `json:"modified"`
	Deleted  int      `json:"deleted"`
	Commit   string   `json:"commit,omitempty"`
	Tree     string   `json:"tree,omitempty"`
	Parent   string   `json:"parent,omitempty"`
	Resolved []string `json:"resolved,omitempty"`
}

func pushDocument(res *app.PushResult, err error) pushDoc {
	return pushDoc{
		Envelope: envelope("push", err),
		DryRun:   res.DryRun,
		NoOp:     res.NoOp,
		Tracked:  res.Tracked,
		Added:    res.Added,
		Modified: res.Modified,
		Deleted:  res.Deleted,
		Commit:   res.Commit,
		Tree:     res.Tree,
		Parent:   res.Parent,
		Resolved: res.Resolved,
	}
}

// --- pull -----------------------------------------------------------------

type pullFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type pullDoc struct {
	Envelope
	DryRun     bool       `json:"dry_run"`
	Writes     int        `json:"writes"`
	Deletes    int        `json:"deletes"`
	RemoteTip  string     `json:"remote_tip,omitempty"`
	Conflicted []string   `json:"conflicted,omitempty"`
	Applied    []pullFile `json:"applied"`
}

func pullDocument(res *app.PullResult, err error) pullDoc {
	doc := pullDoc{
		Envelope:   envelope("pull", err),
		DryRun:     res.DryRun,
		Writes:     res.Writes,
		Deletes:    res.Deletes,
		RemoteTip:  res.RemoteTip,
		Conflicted: res.Conflicted,
	}
	for _, a := range res.Actions {
		switch a.Status {
		case "A", "M", "D", "C":
			doc.Applied = append(doc.Applied, pullFile{Path: a.Path, Status: a.Status})
		}
	}
	if doc.Applied == nil {
		doc.Applied = []pullFile{}
	}
	return doc
}
