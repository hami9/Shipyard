package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func noEnv(string) (string, bool) { return "", false }

func TestRun(t *testing.T) {
	tests := []struct {
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{[]string{"version"}, 0, "shipyard-api dev", ""},
		{[]string{"--version"}, 0, "shipyard-api", ""},
		{[]string{"help"}, 0, "Usage: shipyard-api", ""},
		{nil, 2, "", "Usage: shipyard-api"},
		{[]string{"bogus"}, 2, "", `unknown command "bogus"`},
		{[]string{"serve"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
		{[]string{"migrate"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
		{[]string{"token", "list"}, 1, "", "SHIPYARD_DATABASE_URL: required"},
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

func TestParseTTL(t *testing.T) {
	good := map[string]time.Duration{"1h": time.Hour, "90d": 90 * 24 * time.Hour, "366d": 366 * 24 * time.Hour, "36h": 36 * time.Hour}
	for in, want := range good {
		if got, err := parseTTL(in); err != nil || got != want {
			t.Errorf("parseTTL(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "59m", "367d", "-1d", "d", "1.5d", "forever"} {
		if _, err := parseTTL(in); err == nil {
			t.Errorf("parseTTL(%q) succeeded, want error", in)
		}
	}
}

func TestParseTokenCreate(t *testing.T) {
	o, err := parseTokenCreate(nil)
	if err != nil || o.user != "admin" || o.name != "cli" || len(o.scopes) != 1 || o.scopes[0] != "admin" || o.ttl != 90*24*time.Hour {
		t.Fatalf("defaults = %+v, %v", o, err)
	}
	o, err = parseTokenCreate([]string{"--user", "ops", "--name", "ci", "--scope", "read,deploy", "--ttl", "12h"})
	if err != nil || o.user != "ops" || o.name != "ci" || strings.Join(o.scopes, ",") != "read,deploy" || o.ttl != 12*time.Hour {
		t.Fatalf("flags = %+v, %v", o, err)
	}
	for _, args := range [][]string{{"--scope", "root"}, {"--scope", "read,"}, {"--ttl", "0"}, {"extra"}, {"--bogus"}} {
		if _, err := parseTokenCreate(args); err == nil {
			t.Errorf("parseTokenCreate(%q) succeeded, want error", args)
		}
	}
}
