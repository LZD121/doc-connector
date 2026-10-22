// Package config reads and writes the hidden, human-editable configuration at
// <git-dir>/doc-connector/config.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/LZD121/doc-connector/internal/state"
)

// ErrNotFound is returned by Load when no configuration exists yet.
var ErrNotFound = errors.New("config not found")

// SchemaVersion is the only schema this build understands.
const SchemaVersion = 1

// Defaults applied when a value is absent.
const (
	DefaultDocsDir     = "private-docs"
	DefaultBranch      = "main"
	DefaultWarnBytes   = 50 * 1024 * 1024
	DefaultMaxBytes    = 100 * 1024 * 1024
	DefaultMaxPathChar = 240
)

// DefaultLimits returns the size and path ceilings used for a new config.
func DefaultLimits() Limits {
	return Limits{WarnBytes: DefaultWarnBytes, MaxBytes: DefaultMaxBytes, MaxPathChars: DefaultMaxPathChar}
}

// Project is the identity block.
type Project struct {
	ID           string `yaml:"id"`
	IDSource     string `yaml:"id_source"`
	RootCommit   string `yaml:"root_commit"`
	ObjectFormat string `yaml:"object_format"`
	Adoption     string `yaml:"adoption"`
}

// Ignore holds forward-compatible extra rules.
type Ignore struct {
	Extra []string `yaml:"extra"`
}

// Limits holds size and path ceilings.
type Limits struct {
	WarnBytes    int64 `yaml:"warn_bytes"`
	MaxBytes     int64 `yaml:"max_bytes"`
	MaxPathChars int   `yaml:"max_path_chars"`
}

// Author optionally overrides the synthetic commit identity.
type Author struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// Config is the hidden configuration file.
//
// Unknown keys are preserved verbatim through Extra instead of being rejected:
// a user typo must not make the whole file unreadable, and silently dropping
// the key on the next write would destroy configuration.
type Config struct {
	Schema    int     `yaml:"schema"`
	Remote    string  `yaml:"remote"`
	Branch    string  `yaml:"branch"`
	DocsDir   string  `yaml:"docs_dir"`
	Machine   string  `yaml:"machine"`
	Project   Project `yaml:"project"`
	Ignore    Ignore  `yaml:"ignore"`
	Limits    Limits  `yaml:"limits"`
	Author    *Author `yaml:"author,omitempty"`
	CreatedAt string  `yaml:"created_at"`
	CreatedBy string  `yaml:"created_by"`

	Extra map[string]any `yaml:",inline"`
}

// Dir returns <git-dir>/doc-connector.
func Dir(gitDir string) string { return filepath.Join(gitDir, "doc-connector") }

// FilePath returns the config file path.
func FilePath(gitDir string) string { return filepath.Join(Dir(gitDir), "config.yaml") }

// Load reads and validates the config. warnings lists unknown top-level keys so
// the caller can surface typos on stderr.
func Load(gitDir string) (cfg *Config, warnings []string, err error) {
	data, rerr := os.ReadFile(FilePath(gitDir))
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, rerr
	}
	cfg = &Config{}
	if derr := yaml.Unmarshal(data, cfg); derr != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", FilePath(gitDir), derr)
	}
	for k := range cfg.Extra {
		warnings = append(warnings, fmt.Sprintf("unknown config key %q in %s", k, FilePath(gitDir)))
	}
	cfg.applyDefaults()
	if verr := cfg.Validate(); verr != nil {
		return nil, warnings, verr
	}
	return cfg, warnings, nil
}

// Save writes the config atomically with mode 0600.
func Save(gitDir string, cfg *Config) error {
	cfg.applyDefaults()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(gitDir), 0o700); err != nil {
		return err
	}
	return state.WriteFileAtomic(FilePath(gitDir), buf.Bytes(), 0o600)
}

func (c *Config) applyDefaults() {
	if c.Schema == 0 {
		c.Schema = SchemaVersion
	}
	if c.Branch == "" {
		c.Branch = DefaultBranch
	}
	if c.DocsDir == "" {
		c.DocsDir = DefaultDocsDir
	}
	if c.Limits.WarnBytes == 0 {
		c.Limits.WarnBytes = DefaultWarnBytes
	}
	if c.Limits.MaxBytes == 0 {
		c.Limits.MaxBytes = DefaultMaxBytes
	}
	if c.Limits.MaxPathChars == 0 {
		c.Limits.MaxPathChars = DefaultMaxPathChar
	}
	if c.Project.ObjectFormat == "" {
		c.Project.ObjectFormat = "sha1"
	}
	if c.Project.Adoption == "" {
		c.Project.Adoption = "none"
	}
}

var projectIDPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

// ValidationError reports a broken field with an actionable hint.
type ValidationError struct {
	Msg  string
	Hint string
}

func (e *ValidationError) Error() string    { return e.Msg }
func (e *ValidationError) ExitCode() int    { return 2 }
func (e *ValidationError) HintText() string { return e.Hint }

// Validate checks schema and field invariants that do not require git.
func (c *Config) Validate() error {
	if c.Schema != SchemaVersion {
		return fmt.Errorf("unsupported config schema %d (this build expects %d)", c.Schema, SchemaVersion)
	}
	if strings.TrimSpace(c.Remote) == "" {
		return fmt.Errorf("config: remote must not be empty")
	}
	if EmbeddedCredentials(c.Remote) {
		return &ValidationError{
			Msg:  "config: remote URL contains embedded credentials",
			Hint: "use an SSH URL or a credential helper; doc-connector never stores secrets",
		}
	}
	if !projectIDPattern.MatchString(c.Project.ID) {
		return fmt.Errorf("config: project.id %q is not 12 lowercase hex characters", c.Project.ID)
	}
	if c.Project.IDSource != "derived" && c.Project.IDSource != "explicit" {
		return fmt.Errorf("config: project.id_source must be \"derived\" or \"explicit\", got %q", c.Project.IDSource)
	}
	if !filepath.IsLocal(c.DocsDir) || c.DocsDir == "." {
		return fmt.Errorf("config: docs_dir %q must be a relative path inside the repository", c.DocsDir)
	}
	if strings.TrimSpace(c.Machine) == "" {
		return fmt.Errorf("config: machine must not be empty")
	}
	if c.Branch == "" {
		return fmt.Errorf("config: branch must not be empty")
	}
	if c.Limits.MaxBytes <= 0 || c.Limits.WarnBytes <= 0 || c.Limits.WarnBytes >= c.Limits.MaxBytes {
		return fmt.Errorf("config: limits must satisfy 0 < warn_bytes < max_bytes")
	}
	return nil
}

// EmbeddedCredentials reports whether a remote URL carries a password or token
// in its userinfo. A bare user (`ssh://git@host`, `git@host:path`) is fine.
func EmbeddedCredentials(remote string) bool {
	if i := strings.Index(remote, "://"); i >= 0 {
		scheme := strings.ToLower(remote[:i])
		authority := remote[i+3:]
		if j := strings.IndexByte(authority, '/'); j >= 0 {
			authority = authority[:j]
		}
		at := strings.LastIndexByte(authority, '@')
		if at < 0 {
			return false
		}
		userinfo := authority[:at]
		if strings.Contains(userinfo, ":") {
			return true
		}
		return scheme == "http" || scheme == "https"
	}
	// scp-like user@host:path — only a colon in the user part is a password.
	at := strings.IndexByte(remote, '@')
	if at < 0 {
		return false
	}
	return strings.Contains(remote[:at], ":")
}
