//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/api"
	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
	"github.com/hami9/shipyard/migrations"
)

type cliFixture struct {
	t      *testing.T
	url    string
	token  string
	config string
	s      *store.Store
	seen   bytes.Buffer // every byte the CLI printed, for leak checks
}

func newCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	ctx := t.Context()
	pool, err := store.Open(ctx, storetest.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := store.Migrate(ctx, pool, ms); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	u, _ := s.CreateUser(ctx, "admin")
	plain, prefix, hash := api.NewToken()
	if _, err := s.CreateToken(ctx, store.NewToken{UserID: u.ID, Name: "cli-test", Prefix: prefix, Hash: hash, Scopes: []string{api.ScopeAdmin}}); err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewKeyring("t", map[string][]byte{"t": secrets.GenerateKey()})
	h := api.NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)),
		api.Deps{DB: pool, Tokens: s, TokenAdmin: s, Audit: s, Apps: s, Env: secrets.NewEnv(keys, s), Ops: s, Domains: s, // DNS preflight off
			StreamPoll: 20 * time.Millisecond})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &cliFixture{t: t, url: srv.URL, token: plain, config: filepath.Join(t.TempDir(), "shipyard", "config.json"), s: s}
}

// run executes the CLI and returns its exit code, stdout, and stderr.
func (f *cliFixture) run(stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	e := env{strings.NewReader(stdin), &out, &errb, func(k string) string {
		if k == envConfig {
			return f.config
		}
		return ""
	}}
	code := run(context.Background(), args, e)
	f.seen.WriteString(out.String() + errb.String())
	return code, out.String(), errb.String()
}

func (f *cliFixture) ok(stdin string, args ...string) string {
	f.t.Helper()
	code, out, errb := f.run(stdin, args...)
	if code != 0 {
		f.t.Fatalf("shipyard %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errb)
	}
	return out
}

func TestCLIEndToEnd(t *testing.T) {
	f := newCLIFixture(t)
	const secret = "postgres://u:cli-canary-9e1@db/app"

	// A wrong token is rejected before anything is saved.
	if code, _, errb := f.run("shp_"+strings.Repeat("x", 43)+"\n", "login", "--url", f.url); code != 1 || !strings.Contains(errb, "401") {
		t.Fatalf("login with a bad token: exit %d, %s", code, errb)
	}
	if _, err := os.Stat(f.config); !os.IsNotExist(err) {
		t.Fatal("config saved after a failed login")
	}
	if out := f.ok(f.token+"\n", "login", "--url", f.url); !strings.Contains(out, "Logged in") || strings.Contains(out, f.token) {
		t.Fatalf("login output: %s", out)
	}
	if out := f.ok("", "whoami"); !strings.Contains(out, "cli-test") || !strings.Contains(out, "admin") {
		t.Fatalf("whoami: %s", out)
	}

	f.ok("", "app", "create", "web", "--repo", "hami9/demo", "--branch", "main", "--port", "3000")
	f.ok("", "app", "create", "--repo", "hami9/api", "api", "--port", "8080", "--branch", "main", "--health-path", "/healthz")
	code, _, errb := f.run("", "app", "create", "Bad_Slug", "--repo", "x", "--branch", "-x", "--port", "0")
	if code != 1 || !strings.Contains(errb, "422") || !strings.Contains(errb, "slug:") || !strings.Contains(errb, "branch: must not start with '-'") {
		t.Fatalf("invalid app: exit %d\n%s", code, errb)
	}
	if out := f.ok("", "ps"); !regexp.MustCompile(`(?m)^api\s+hami9/api\s+main\s+8080$`).MatchString(out) || !strings.Contains(out, "web") {
		t.Fatalf("ps:\n%s", out)
	}
	if out := f.ok("", "app", "show", "api"); !strings.Contains(out, "/healthz (timeout 1m0s)") || !regexp.MustCompile(`auto deploy\s+false`).MatchString(out) {
		t.Fatalf("app show:\n%s", out)
	}
	// P4.3: what a push deploys is the branch and the auto-deploy switch.
	if out := f.ok("", "app", "update", "api", "--auto-deploy", "--branch", "release"); out != "Updated app api (branch release, deploys on push).\n" {
		t.Fatalf("app update: %q", out)
	}
	if out := f.ok("", "app", "update", "api", "--auto-deploy=false"); out != "Updated app api (branch release).\n" {
		t.Fatalf("app update --auto-deploy=false: %q", out)
	}
	if code, _, _ := f.run("", "app", "update", "api"); code != 2 {
		t.Fatalf("app update without a setting: exit %d, want 2 (usage)", code)
	}
	if out := f.ok("", "app", "create", "hook", "--repo", "hami9/hook", "--branch", "main", "--port", "80", "--auto-deploy"); !strings.Contains(out, "deploys on push") {
		t.Fatalf("app create --auto-deploy: %q", out)
	}
	f.ok("", "app", "update", "api", "--branch", "main")
	// P4.4: the GitHub App installation the worker fetches a private repository through.
	if out := f.ok("", "app", "show", "api"); !regexp.MustCompile(`github app\s+none \(public repository\)`).MatchString(out) {
		t.Fatalf("app show before an installation:\n%s", out)
	}
	f.ok("", "app", "update", "api", "--github-installation", "4242")
	if out := f.ok("", "app", "show", "api"); !regexp.MustCompile(`github app\s+installation 4242`).MatchString(out) {
		t.Fatalf("app show after --github-installation:\n%s", out)
	}

	f.ok(secret+"\n", "env", "set", "web", "DATABASE_URL")
	f.ok("info\n", "env", "set", "web", "LOG_LEVEL", "--plain")
	out := f.ok("", "env", "list", "web")
	if !strings.Contains(out, "revision 2") || !regexp.MustCompile(`DATABASE_URL\s+secret`).MatchString(out) || !regexp.MustCompile(`LOG_LEVEL\s+plain`).MatchString(out) {
		t.Fatalf("env list:\n%s", out)
	}
	f.ok("", "env", "unset", "web", "LOG_LEVEL")
	if code, _, errb := f.run("", "env", "unset", "web", "LOG_LEVEL"); code != 1 || !strings.Contains(errb, "404") {
		t.Fatalf("unset missing key: %d %s", code, errb)
	}

	if out := f.ok("", "domain", "add", "web", "Web.Example.com"); !strings.Contains(out, "Added web.example.com to web (no active deployment yet") {
		t.Fatalf("domain add:\n%s", out)
	}
	if out := f.ok("", "domain", "list", "web"); !regexp.MustCompile(`(?m)^web\.example\.com\s+-\s+skipped$`).MatchString(out) {
		t.Fatalf("domain list:\n%s", out)
	}
	if code, _, errb := f.run("", "domain", "add", "api", "web.example.com"); code != 1 || !strings.Contains(errb, "already used by an app") {
		t.Fatalf("duplicate domain: %d %s", code, errb)
	}
	f.ok("", "domain", "remove", "web", "web.example.com")
	if code, _, errb := f.run("", "domain", "remove", "web", "web.example.com"); code != 1 || !strings.Contains(errb, "404") {
		t.Fatalf("remove twice: %d %s", code, errb)
	}

	out = f.ok("", "deploy", "web")
	m := regexp.MustCompile(`operation: ([0-9a-f-]{36}) \(queued\)`).FindStringSubmatch(out)
	if !strings.Contains(out, "Queued deploy of web at the head of the tracked branch") || m == nil {
		t.Fatalf("deploy:\n%s", out)
	}
	sha := strings.Repeat("ab", 20)
	out = f.ok("", "deploy", "web", "--ref", sha, "--idempotency-key", "ci-42")
	if !strings.Contains(out, "cancelled older queued operation "+m[1]) {
		t.Fatalf("second deploy did not supersede the first:\n%s", out)
	}
	if out := f.ok("", "deploy", "web", "--ref", sha, "--idempotency-key", "ci-42"); !strings.Contains(out, "Already requested") {
		t.Fatalf("replayed deploy:\n%s", out)
	}
	if code, _, errb := f.run("", "deploy", "web", "--ref", "abc"); code != 1 || !strings.Contains(errb, "ref: must be a full commit SHA") {
		t.Fatalf("short ref: %d %s", code, errb)
	}
	if out := f.ok("", "operation", m[1]); !regexp.MustCompile(`status\s+cancelled`).MatchString(out) || !strings.Contains(out, "superseded by") {
		t.Fatalf("operation:\n%s", out)
	}
	// P3.1: no deployment ran yet (the queued ones were cancelled first).
	if out := f.ok("", "releases", "web"); out != "No releases.\n" {
		t.Fatalf("releases:\n%s", out)
	}
	if code, _, errb := f.run("", "releases", "web", "--limit", "101"); code != 1 || !strings.Contains(errb, "limit: must be") {
		t.Fatalf("releases --limit 101: %d %s", code, errb)
	}
	// P2.7: events streams the log to the end; a cancelled operation fails.
	for _, msg := range []string{"fetching", "two\nlines"} {
		if _, err := f.s.AppendOperationEvent(t.Context(), m[1], store.LevelWarn, msg); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errb = f.run("", "events", m[1])
	if code != 1 || !strings.Contains(errb, "operation "+m[1]+" cancelled: superseded by") ||
		!regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d warn  fetching\n\d\d:\d\d:\d\d warn  two\n {15}lines\n$`).MatchString(out) {
		t.Fatalf("events: exit %d\nstdout:\n%s\nstderr: %s", code, out, errb)
	}

	// P3.8: app delete wants --yes, then queues the delete; a second request
	// finds the same operation, and the app takes no deploy meanwhile.
	if code, out, errb := f.run("", "app", "delete", "api"); code != 1 || out != "" || !strings.Contains(errb, "cannot be undone. Repeat with --yes") {
		t.Fatalf("app delete without --yes: %d\n%s%s", code, out, errb)
	}
	queued := f.ok("", "app", "delete", "api", "--yes")
	dm := regexp.MustCompile(`^Queued the delete of api\.\noperation: ([0-9a-f-]{36}) \(queued\)\n$`).FindStringSubmatch(queued)
	if dm == nil {
		t.Fatalf("app delete:\n%s", queued)
	}
	if again := f.ok("", "app", "delete", "api", "--yes"); !strings.Contains(again, "Already in progress: the delete of api.") || !strings.Contains(again, dm[1]) {
		t.Fatalf("second app delete:\n%s", again)
	}
	if code, _, errb := f.run("", "deploy", "api"); code != 1 || !strings.Contains(errb, "409") || !strings.Contains(errb, "being deleted") {
		t.Fatalf("deploy while deleting: %d %s", code, errb)
	}
	if code, _, errb := f.run("", "app", "delete", "nope", "--yes"); code != 1 || !strings.Contains(errb, "404") {
		t.Fatalf("delete of an unknown app: %d %s", code, errb)
	}

	// Negative: neither the token nor the secret value was ever printed.
	for name, leak := range map[string]string{"token": f.token, "secret": "cli-canary-9e1"} {
		if strings.Contains(f.seen.String(), leak) {
			t.Fatalf("the %s appeared in CLI output", name)
		}
	}
}

// ADR-0011: token list, revoke, and rotate through the API. rotate saves
// the new token to the config file and never prints it; with
// SHIPYARD_TOKEN it prints it instead, since the variable cannot be changed.
func TestCLITokens(t *testing.T) {
	f := newCLIFixture(t)
	ctx := t.Context()
	f.ok(f.token+"\n", "login", "--url", f.url)
	u, _ := f.s.UserByName(ctx, "admin")
	readPlain, readPrefix, readHash := api.NewToken()
	if _, err := f.s.CreateToken(ctx, store.NewToken{UserID: u.ID, Name: "viewer", Prefix: readPrefix, Hash: readHash, Scopes: []string{api.ScopeRead}}); err != nil {
		t.Fatal(err)
	}
	if out := f.ok("", "token", "list"); !regexp.MustCompile(`(?m)^shp_\S+\s+cli-test\s+admin\s+-\s+\S+\s+active$`).MatchString(out) || !strings.Contains(out, "viewer") {
		t.Fatalf("token list:\n%s", out)
	}

	out := f.ok("", "token", "rotate")
	if !strings.Contains(out, "The old token is revoked.") || !strings.Contains(out, "Saved to "+f.config) {
		t.Fatalf("token rotate: %q", out)
	}
	cfg, err := readConfigFile(f.config)
	if err != nil || cfg.Token == f.token || !strings.HasPrefix(cfg.Token, api.TokenPrefix) || cfg.URL != f.url {
		t.Fatalf("config after rotate = %+v, %v", cfg, err)
	}
	if strings.Contains(f.seen.String(), cfg.Token) {
		t.Fatal("the saved token was printed")
	}
	// The saved token works; the old one is gone.
	if out := f.ok("", "whoami"); !strings.Contains(out, "cli-test") {
		t.Fatalf("whoami after rotate: %s", out)
	}
	if _, err := f.s.ActiveTokenByHash(ctx, api.HashToken(f.token)); err == nil {
		t.Fatal("the rotated token still authenticates")
	}

	// With SHIPYARD_TOKEN, a read token rotates itself and gets the token printed.
	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"token", "rotate", "--grace", "1h"}, env{strings.NewReader(""), &stdout, &stderr, func(k string) string {
		return map[string]string{envConfig: f.config, envToken: readPlain}[k]
	}})
	printed := strings.TrimSpace(stdout.String())
	if code != 0 || !strings.HasPrefix(printed, api.TokenPrefix) || !strings.Contains(stderr.String(), "SHIPYARD_TOKEN is set") ||
		!strings.Contains(stderr.String(), "keeps working until") {
		t.Fatalf("rotate with SHIPYARD_TOKEN: exit %d\nstdout %q\nstderr %q", code, stdout.String(), stderr.String())
	}
	if after, _ := readConfigFile(f.config); after.Token != cfg.Token {
		t.Fatal("rotating an environment token changed the config file")
	}

	// Negative: list and revoke need admin; revoke of an unknown prefix is 404.
	code, _, errb := f.run("", "token", "list")
	if code != 0 {
		t.Fatalf("token list as admin: exit %d %s", code, errb)
	}
	stdout.Reset()
	stderr.Reset()
	code = run(ctx, []string{"token", "list"}, env{strings.NewReader(""), &stdout, &stderr, func(k string) string {
		return map[string]string{envConfig: f.config, envToken: printed}[k]
	}})
	if code != 1 || !strings.Contains(stderr.String(), "403") {
		t.Fatalf("token list with a read token: exit %d %s", code, stderr.String())
	}
	if code, _, errb := f.run("", "token", "revoke", "shp_nothere1"); code != 1 || !strings.Contains(errb, "404") {
		t.Fatalf("revoke unknown: exit %d %s", code, errb)
	}
	if out := f.ok("", "token", "revoke", readPrefix); !strings.Contains(out, "Revoked "+readPrefix+" (viewer)") {
		t.Fatalf("revoke: %q", out)
	}
	if out := f.ok("", "token", "list"); !regexp.MustCompile(readPrefix + `\s+viewer\s+read\s+.*revoked`).MatchString(out) {
		t.Fatalf("token list after revoke:\n%s", out)
	}
	if code, _, _ := f.run("", "token", "rotate", "extra"); code != 2 {
		t.Fatalf("token rotate with an argument: exit %d, want 2", code)
	}
}
