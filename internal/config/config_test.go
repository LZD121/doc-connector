package config

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

const sample = `schema: 1
remote: "git@github.com:me/private-docs.git"
branch: "main"
docs_dir: "private-docs"
machine: "laptop"
project:
  id: "743c420a8e7b"
  id_source: "derived"
  root_commit: "743c420a8e7b11c3382c49dfec71987cb7a6f710"
  object_format: "sha1"
  adoption: "none"
ignore:
  extra: []
limits:
  warn_bytes: 52428800
  max_bytes: 104857600
  max_path_chars: 240
created_at: "2026-10-22T12:00:00Z"
created_by: "laptop"
remot: "typo.example"
`

func TestLoadSavePreservesUnknownKeys(t *testing.T) {
	gitDir := t.TempDir()
	if err := os.MkdirAll(Dir(gitDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FilePath(gitDir), []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, warnings, err := Load(gitDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"remot"`) {
		t.Errorf("warnings = %v, want one naming remot", warnings)
	}
	if cfg.Project.ID != "743c420a8e7b" {
		t.Errorf("id = %q", cfg.Project.ID)
	}
	if err := Save(gitDir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(FilePath(gitDir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "remot: typo.example") {
		t.Errorf("unknown key was dropped:\n%s", data)
	}
	info, err := os.Stat(FilePath(gitDir))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("config mode = %o, want 600", perm)
		}
	}
}

func TestLoadMissing(t *testing.T) {
	_, _, err := Load(t.TempDir())
	if err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEmbeddedCredentials(t *testing.T) {
	cases := map[string]bool{
		"git@github.com:me/docs.git":               false,
		"ssh://git@github.com/me/docs.git":         false,
		"git://github.com/me/docs.git":             false,
		"https://github.com/me/docs.git":           false,
		"https://token@github.com/me/docs.git":     true,
		"https://user:pass@github.com/me/docs.git": true,
		"user:pass@example.com:path/docs.git":      true,
		"file:///tmp/remote.git":                   false,
	}
	for remote, want := range cases {
		if got := EmbeddedCredentials(remote); got != want {
			t.Errorf("EmbeddedCredentials(%q) = %v, want %v", remote, got, want)
		}
	}
}

func TestValidateRejectsBadIdentity(t *testing.T) {
	cfg := &Config{
		Schema:  1,
		Remote:  "git@github.com:me/docs.git",
		Branch:  "main",
		DocsDir: "private-docs",
		Machine: "laptop",
		Project: Project{ID: "ABCDEFABCDEF", IDSource: "derived"},
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("uppercase id accepted")
	}
	cfg.Project.ID = "743c420a8e7b"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cfg.DocsDir = "../outside"
	if err := cfg.Validate(); err == nil {
		t.Fatal("escaping docs_dir accepted")
	}
}
