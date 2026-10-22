package machine

import "testing"

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"MacBook-Pro.local": "macbook-pro-local",
		"DESKTOP_1234":      "desktop-1234",
		"  weird  name  ":   "weird-name",
		"":                  "unknown",
		"!!!":               "unknown",
		"a":                 "a",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
