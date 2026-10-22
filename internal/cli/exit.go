package cli

import "fmt"

// Exit codes are a closed set fixed by REQUIREMENTS.md CLI-02.
//
// Deliberately absent: the "5" used by some research documents for git
// subprocess or local I/O failure. Network/authentication failures map to 4;
// every other unexpected failure maps to 1. Keeping one table avoids the three
// competing mappings that appear in the research notes.
const (
	CodeOK          = 0   // success, including "already up to date"
	CodeConflict    = 1   // conflict or manual decision required
	CodeUsage       = 2   // usage, identity or environment error
	CodeGuard       = 3   // isolation (tracked docs, unsafe path) or lock failure
	CodeRemote      = 4   // network or authentication failure
	CodeInterrupted = 130 // interrupted by SIGINT
)

// Error is a CLI-facing failure with a stable exit code.
type Error struct {
	Code int
	Msg  string
	Hint string
	Err  error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

// ExitCode implements the coded interface used by wrap.
func (e *Error) ExitCode() int { return e.Code }

// HintText implements the coded interface used by wrap.
func (e *Error) HintText() string { return e.Hint }

// Errorf builds a CLI error with an exit code.
func Errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// WithHint attaches an actionable hint (not an unverified fix suggestion).
func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

// coded is implemented by domain errors that know their exit code.
type coded interface {
	error
	ExitCode() int
	HintText() string
}

// Wrap converts an arbitrary error into a CLI error, preserving a domain
// error's exit code and otherwise using fallback.
func Wrap(err error, fallback int) error {
	if err == nil {
		return nil
	}
	if c, ok := err.(coded); ok {
		return &Error{Code: c.ExitCode(), Msg: c.Error(), Hint: c.HintText(), Err: err}
	}
	return &Error{Code: fallback, Msg: err.Error(), Err: err}
}
