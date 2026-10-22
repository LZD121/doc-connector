package gitstore

import "strings"

// remoteMarkers are stable English fragments (LC_ALL=C) produced for network
// or authentication failures.
var remoteMarkers = []string{
	"could not read username",
	"could not read password",
	"permission denied",
	"host key verification failed",
	"authentication failed",
	"unable to access",
	"could not resolve host",
	"connection refused",
	"connection timed out",
	"terminal prompts disabled",
	"the requested url returned error",
	"repository not found",
	"no such file or directory",
	"couldn't find remote ref",
	"remote end hung up",
}

// raceMarkers indicate the remote moved while we were building a push.
var raceMarkers = []string{
	"stale info",
	"non-fast-forward",
	"failed to push some refs",
	"[rejected]",
	"fetch first",
	"cannot lock ref",
}

// Classify maps git's stderr and exit status onto a Kind. It is the only place
// that matches git diagnostics; the application layer branches on Kind.
func Classify(stderr string, exitCode int) Kind {
	s := strings.ToLower(stderr)
	for _, m := range raceMarkers {
		if strings.Contains(s, m) {
			return KindRace
		}
	}
	for _, m := range remoteMarkers {
		if strings.Contains(s, m) {
			return KindRemote
		}
	}
	return KindOther
}
