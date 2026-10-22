package fsx

import (
	"strings"
	"testing"
)

func TestCaseCollisions(t *testing.T) {
	if err := CheckCaseCollisions([]string{"a.md", "notes/b.md"}); err != nil {
		t.Fatalf("distinct paths rejected: %v", err)
	}
	err := CheckCaseCollisions([]string{"A.md", "a.md", "notes/B.md"})
	if err == nil {
		t.Fatal("case collision accepted")
	}
	fe, ok := err.(*Error)
	if !ok || fe.ExitCode() != 3 {
		t.Fatalf("error = %v", err)
	}
	for _, want := range []string{"A.md", "a.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message omits %q: %q", want, err.Error())
		}
	}

	// NFC vs NFD forms of the same name must collide.
	nfc := "café.md"
	nfd := "cafe\u0301.md"
	if err := CheckCaseCollisions([]string{nfc, nfd}); err == nil {
		t.Fatal("NFC/NFD collision accepted")
	}
	if non := NonNFCPaths([]string{nfc, nfd}); len(non) != 1 || non[0] != nfd {
		t.Errorf("NonNFCPaths = %v", non)
	}
}

func TestValidateSyncPathReserved(t *testing.T) {
	for _, p := range []string{"NUL", "nul.txt", "CON.md", "dir/LPT1.log", "name.", "name "} {
		if err := ValidateSyncPath(p, 200); err == nil {
			t.Errorf("ValidateSyncPath(%q) accepted", p)
		}
	}
	if err := ValidateSyncPath("normal/path.md", 200); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}
	if err := ValidateSyncPath(strings.Repeat("a", 300), 240); err == nil {
		t.Error("over-long path accepted")
	}
}
