package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testEnv(stdin string, vars map[string]string) (env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return env{strings.NewReader(stdin), &out, &errb, func(k string) string { return vars[k] }}, &out, &errb
}

func TestRunBasics(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "none.json")
	tests := []struct {
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{[]string{"version"}, 0, "shipyard dev", ""},
		{[]string{"help"}, 0, "Usage: shipyard", ""},
		{nil, 2, "", "Usage: shipyard"},
		{[]string{"bogus"}, 2, "", `unknown command "bogus"`},
		{[]string{"whoami"}, 1, "", "no API URL configured"},
		{[]string{"login"}, 2, "", "Usage: shipyard"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			e, out, errb := testEnv("", map[string]string{envConfig: cfg})
			if code := run(context.Background(), tt.args, e); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr %s)", code, tt.wantCode, errb)
			}
			if !strings.Contains(out.String(), tt.wantStdout) || !strings.Contains(errb.String(), tt.wantStderr) {
				t.Errorf("stdout = %q, stderr = %q", out, errb)
			}
		})
	}
}

func TestParseInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	repo := fs.String("repo", "", "")
	port := fs.Int("port", 0, "")
	pos, err := parse(fs, []string{"--repo", "a/b", "web", "--port", "80"}, 1)
	if err != nil || len(pos) != 1 || pos[0] != "web" || *repo != "a/b" || *port != 80 {
		t.Fatalf("parse = %v %v (repo %q port %d)", pos, err, *repo, *port)
	}
	for _, args := range [][]string{{}, {"a", "b"}, {"web", "--bogus"}} {
		if _, err := parse(flag.NewFlagSet("y", flag.ContinueOnError), args, 1); err == nil {
			t.Errorf("parse(%q) accepted", args)
		}
	}
}

func TestReadSecret(t *testing.T) {
	for in, want := range map[string]string{"v\n": "v", "v\r\n": "v", "v": "v", "a\nb\n": "a\nb", "\n": "", " spaced \n": " spaced "} {
		e, _, _ := testEnv(in, nil)
		if got, err := readSecret(e, "x"); err != nil || got != want {
			t.Errorf("readSecret(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	if err := saveConfig(path, config{URL: "https://api.example.com", Token: "shp_x"}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("config mode %v, want 0600", fi.Mode().Perm())
		}
	}
	c, err := loadConfig(path, func(string) string { return "" })
	if err != nil || c.URL != "https://api.example.com" || c.Token != "shp_x" {
		t.Fatalf("loadConfig = %+v, %v", c, err)
	}
	c, _ = loadConfig(path, func(k string) string {
		return map[string]string{envURL: "https://ci.example.com", envToken: "shp_ci"}[k]
	})
	if c.URL != "https://ci.example.com" || c.Token != "shp_ci" {
		t.Fatalf("env overrides = %+v", c)
	}
	if runtime.GOOS != "windows" {
		os.Chmod(path, 0o644)
		if _, err := loadConfig(path, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("world-readable config accepted: %v", err)
		}
	}
}
