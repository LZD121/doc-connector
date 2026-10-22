// Package machine derives the human-stable machine identity used for conflict
// artifact suffixes and commit trailers.
package machine

import (
	"os"
	"strings"
)

const maxLen = 32

// Sanitize lowercases a hostname, folds every non [a-z0-9] run into a single
// dash, trims dashes, and truncates to 32 characters. An empty result becomes
// "unknown" so a suffix can never be empty.
func Sanitize(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxLen {
		out = strings.Trim(out[:maxLen], "-")
	}
	if out == "" {
		return "unknown"
	}
	return out
}

// Hostname returns the sanitized local hostname, or "unknown" if unavailable.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return Sanitize(h)
}
