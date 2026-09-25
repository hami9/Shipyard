package main

import (
	"bytes"
	"strings"
	"testing"
)

func noEnv(string) (string, bool) { return "", false }

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
