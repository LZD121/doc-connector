package state

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteFileAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Errorf("content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("mode = %o, want 600", perm)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if len(matches) != 0 {
		t.Errorf("temporary files left behind: %v", matches)
	}
}

func TestWriteFileAtomicFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(target, []byte("x"), 0o600); err == nil {
		t.Fatal("expected failure renaming over a directory")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if len(matches) != 0 {
		t.Errorf("temporary files left behind: %v", matches)
	}
}

func TestStateRoundTrip(t *testing.T) {
	gitDir := t.TempDir()
	st := State{
		ProjectID:    "743c420a8e7b",
		Branch:       "main",
		SyncedCommit: "abc123",
		Unresolved: []Conflict{{
			Path: "notes/plan.md", Kind: "both-modified",
			Theirs: "notes/plan.md.theirs.laptop", RemoteOID: "def",
		}},
	}
	if err := Save(gitDir, st); err != nil {
		t.Fatal(err)
	}
	got, err := Load(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != st.ProjectID || got.SyncedCommit != st.SyncedCommit || len(got.Unresolved) != 1 {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if got.Unresolved[0].Theirs != "notes/plan.md.theirs.laptop" {
		t.Errorf("conflict lost: %+v", got.Unresolved[0])
	}

	empty, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if empty.ProjectID != "" || empty.Schema != SchemaVersion {
		t.Errorf("empty state = %+v", empty)
	}
}

func TestFetchedAndJournal(t *testing.T) {
	gitDir := t.TempDir()
	if err := SaveFetched(gitDir, NewFetched("abc", "status")); err != nil {
		t.Fatal(err)
	}
	if f := LoadFetched(gitDir); f.Commit != "abc" || f.By != "status" {
		t.Errorf("fetched = %+v", f)
	}

	j := &Journal{Op: "pull", ProjectID: "743c420a8e7b", Ops: []JournalOp{{Action: "write", Path: "a.md"}}}
	if err := SaveJournal(gitDir, j); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadJournal(gitDir)
	if err != nil || loaded == nil {
		t.Fatalf("LoadJournal: %v, %v", loaded, err)
	}
	if len(loaded.Ops) != 1 || loaded.Ops[0].Path != "a.md" {
		t.Errorf("journal = %+v", loaded)
	}
	if err := RemoveJournal(gitDir); err != nil {
		t.Fatal(err)
	}
	if again, err := LoadJournal(gitDir); err != nil || again != nil {
		t.Errorf("journal not removed: %+v, %v", again, err)
	}
}

func TestCheckIdentity(t *testing.T) {
	st := State{ProjectID: "743c420a8e7b", Branch: "main"}
	if err := st.CheckIdentity("743c420a8e7b", "main"); err != nil {
		t.Errorf("matching identity rejected: %v", err)
	}
	err := st.CheckIdentity("111111111111", "main")
	if err == nil {
		t.Fatal("mismatched project accepted")
	}
	if pe := err.(*Error); pe.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2", pe.ExitCode())
	}
	if err := (State{}).CheckIdentity("111111111111", "main"); err != nil {
		t.Errorf("first run rejected: %v", err)
	}
}

func TestLockContention(t *testing.T) {
	gitDir := t.TempDir()
	ctx := context.Background()
	first, err := Acquire(ctx, gitDir, "init", "laptop")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first.Release()

	waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_, err = Acquire(waitCtx, gitDir, "push", "laptop")
	if err == nil {
		t.Fatal("second acquire succeeded while lock was held")
	}
	se, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if se.ExitCode() != 3 {
		t.Errorf("exit = %d, want 3", se.ExitCode())
	}
	if !strings.Contains(se.Error(), "lock") {
		t.Errorf("message = %q", se.Error())
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(ctx, gitDir, "push", "laptop")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	second.Release()

	if _, err := os.Stat(LockPath(gitDir)); err != nil {
		t.Errorf("lock file removed: %v", err)
	}
}

func TestProbe(t *testing.T) {
	ctx := context.Background()
	gitDir := t.TempDir()

	if h, found, err := Probe(gitDir); found || err != nil || h != (Holder{}) {
		t.Fatalf("absent lock = %+v, found=%v, err=%v", h, found, err)
	}

	if err := os.MkdirAll(Dir(gitDir), 0o700); err != nil {
		t.Fatal(err)
	}
	holderPath := HolderPath(gitDir)
	if err := os.WriteFile(holderPath, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h, found, err := Probe(gitDir); found || err != nil {
		t.Errorf("empty record = %+v, found=%v, err=%v; want not found", h, found, err)
	}

	if err := os.WriteFile(holderPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h, found, err := Probe(gitDir); !found || err == nil || h != (Holder{}) {
		t.Errorf("malformed record = %+v, found=%v, err=%v; want the zero Holder, found and an error", h, found, err)
	}

	if err := os.Remove(holderPath); err != nil {
		t.Fatal(err)
	}
	held, err := Acquire(ctx, gitDir, "pull", "laptop")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	holder, found, err := Probe(gitDir)
	if err != nil || !found {
		t.Fatalf("probe while held = %+v, found=%v, err=%v", holder, found, err)
	}
	if holder.PID != os.Getpid() || holder.Cmd != "pull" || holder.Machine != "laptop" || holder.Schema != SchemaVersion {
		t.Errorf("holder = %+v", holder)
	}
	if holder.StartedAt == "" {
		t.Error("holder has no started_at")
	}

	// Probing must leave the holder untouched: release still works and the lock
	// can be taken again immediately.
	if err := held.Release(); err != nil {
		t.Fatalf("release after probe: %v", err)
	}
	again, err := Acquire(ctx, gitDir, "push", "laptop")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
	if stale, found, err := Probe(gitDir); err != nil || !found || stale.Cmd != "push" {
		t.Errorf("record after release = %+v, found=%v, err=%v", stale, found, err)
	}
}

// TryAcquire is a question, not a claim: a probe must not replace the record of
// the process that actually wrote last.
func TestTryAcquireLeavesRecord(t *testing.T) {
	ctx := context.Background()
	gitDir := t.TempDir()

	held, err := Acquire(ctx, gitDir, "push", "laptop")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Probing the record while the lock is held is the Windows-sensitive part:
	// the record must come from a file the flock does not lock.
	if holder, found, perr := Probe(gitDir); perr != nil || !found || holder.Cmd != "push" {
		t.Fatalf("probe while held = %+v, found=%v, err=%v", holder, found, perr)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}

	probe, err := TryAcquire(ctx, gitDir)
	if err != nil {
		t.Fatalf("try acquire: %v", err)
	}
	if err := probe.Release(); err != nil {
		t.Fatal(err)
	}
	if holder, found, perr := Probe(gitDir); perr != nil || !found || holder.Cmd != "push" || holder.Machine != "laptop" {
		t.Errorf("record after probe = %+v, found=%v, err=%v; want the push record", holder, found, perr)
	}
}
