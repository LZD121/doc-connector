package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/LZD121/doc-connector/internal/app"
)

// doctorDoc is the JSON form of one diagnostic run.
type doctorDoc struct {
	Envelope
	Checks            []app.Check       `json:"checks"`
	Summary           app.DoctorSummary `json:"summary"`
	RemoteUnreachable bool              `json:"remote_unreachable,omitempty"`
}

func cmdDoctor(ctx context.Context, e *runnerEnv, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print one JSON document")

	helped, err := parseFlags(fs, args, e.Stdout, doctorUsage)
	if err != nil {
		return err
	}
	if helped {
		return nil
	}
	if err := noPositional(fs); err != nil {
		return err
	}

	res, derr := app.Doctor(ctx, app.Env{Cwd: e.Cwd, Stdout: e.Stdout, Stderr: e.Stderr})
	if res == nil {
		return Wrap(derr, CodeConflict)
	}

	if *jsonOut {
		if err := e.emit(doctorDocument(res)); err != nil {
			return err
		}
	} else {
		for _, c := range res.Checks {
			fmt.Fprintf(e.Stdout, "[%s] %s: %s\n", c.Status, c.ID, c.Detail)
			if c.Hint != "" {
				fmt.Fprintf(e.Stdout, "        hint: %s\n", c.Hint)
			}
		}
		fmt.Fprintf(e.Stdout, "summary: %d ok, %d warn, %d fail\n", res.Summary.OK, res.Summary.Warn, res.Summary.Fail)
	}

	if derr != nil {
		return Wrap(derr, CodeConflict)
	}
	return doctorExit(res)
}

// doctorDocument mirrors the human report. A diagnostic run is not itself a
// failure, so ok is true even when checks failed; the exit code carries that.
func doctorDocument(res *app.DoctorResult) doctorDoc {
	doc := doctorDoc{
		Envelope:          envelope("doctor", nil),
		Checks:            res.Checks,
		Summary:           res.Summary,
		RemoteUnreachable: res.RemoteUnreachable,
	}
	if doc.Checks == nil {
		doc.Checks = []app.Check{}
	}
	return doc
}

// doctorExit maps findings onto the closed exit-code set: an unreachable remote
// is a network problem (4), any other failure is an isolation or environment
// problem (3), and warnings alone still exit 0.
func doctorExit(res *app.DoctorResult) error {
	switch {
	case res.RemoteUnreachable:
		return Errorf(CodeRemote, "doctor found %d failing check(s)", res.Summary.Fail).
			WithHint("the private remote is not reachable from this machine; run \"doc-connector status\" once the network or credentials are fixed")
	case res.Summary.Fail > 0:
		return Errorf(CodeGuard, "doctor found %d failing check(s)", res.Summary.Fail).
			WithHint("read the failing check details above; each one names the path or setting to fix")
	}
	return nil
}
