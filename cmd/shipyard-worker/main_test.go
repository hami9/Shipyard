package main

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/app"
)

func noEnv(string) (string, bool) { return "", false }

// P3.7: the crash hook accepts only real fault points, and stays quiet at
// every other point.
func TestCrashAt(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := crashAt("nowhere", log); err == nil || !strings.Contains(err.Error(), "no such fault point") {
		t.Fatalf("unknown point: %v", err)
	}
	fault, err := crashAt(app.FaultCommitted, log)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range app.FaultPoints {
		if p != app.FaultCommitted {
			fault(p) // must return
		}
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{[]string{"version"}, 0, "shipyard-worker dev", ""},
		{[]string{"--version"}, 0, "shipyard-worker", ""},
		{[]string{"help"}, 0, "Usage: shipyard-worker", ""},
		{nil, 2, "", "Usage: shipyard-worker"},
		{[]string{"bogus"}, 2, "", `unknown command "bogus"`},
		{[]string{"run"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
		{[]string{"backup"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
		// restore takes exactly one backup directory.
		{[]string{"restore"}, 2, "", "restore needs --from DIR"},
		{[]string{"restore", "--from"}, 2, "", "restore needs --from DIR"},
		{[]string{"restore", "--from="}, 2, "", "restore needs --from DIR"},
		{[]string{"restore", "--from", "/a", "/b"}, 2, "", "restore needs --from DIR"},
		{[]string{"restore", "--from", "/var/backups/shipyard/x"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
		{[]string{"restore", "--from=/var/backups/shipyard/x"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
		// kek takes exactly status or rewrap (ADR-0012).
		{[]string{"kek"}, 2, "", "kek needs status or rewrap"},
		{[]string{"kek", "rotate"}, 2, "", "kek needs status or rewrap"},
		{[]string{"kek", "status", "extra"}, 2, "", "kek needs status or rewrap"},
		{[]string{"kek", "status"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
		{[]string{"kek", "rewrap"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, noEnv, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
