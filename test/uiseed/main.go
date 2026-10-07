// Command uiseed prepares and removes the data the web UI's Playwright tests
// run against (P6.6, web/e2e). It is test tooling, never shipped.
//
//	uiseed create KEKDIR   a fresh database, migrated and seeded, and an HPKE
//	                       public key in KEKDIR; prints JSON for the tests
//	uiseed drop NAME       drop that database
//
// SHIPYARD_TEST_DATABASE_URL is the admin connection, as for the
// integration tests (internal/store/storetest).
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hami9/shipyard/internal/api"
	"github.com/hami9/shipyard/internal/routing"
	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
	"github.com/hami9/shipyard/migrations"
)

// KEKID is the active KEK the tests run the API with. Only its public key
// is written: the API seals with it and never decrypts (ADR-0012), and a
// public key has no file-mode check, so this works on Windows too.
const KEKID = "uitest"

type output struct {
	Database    string            `json:"database"`
	DatabaseURL string            `json:"database_url"`
	KEKID       string            `json:"kek_id"`
	Tokens      map[string]string `json:"tokens"` // by scope
	// CSP is what Caddy serves the UI with, so the tests serve it the same.
	CSP string `json:"csp"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "uiseed:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	admin := os.Getenv(storetest.EnvURL)
	if admin == "" {
		return fmt.Errorf("%s is not set (make dev-up)", storetest.EnvURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch {
	case len(args) == 2 && args[0] == "create":
		out, err := create(ctx, admin, args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	case len(args) == 2 && args[0] == "drop" && strings.HasPrefix(args[1], "shipyard_ui_"):
		return exec(ctx, admin, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{args[1]}.Sanitize()))
	}
	return fmt.Errorf("usage: uiseed create KEKDIR | uiseed drop shipyard_ui_…")
}

func exec(ctx context.Context, url, sql string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql)
	return err
}

func create(ctx context.Context, admin, kekDir string) (output, error) {
	name := "shipyard_ui_" + strings.ToLower(rand.Text()[:12])
	if err := exec(ctx, admin, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return output{}, fmt.Errorf("create database: %w", err)
	}
	u, err := url.Parse(admin)
	if err != nil {
		return output{}, err
	}
	u.Path = "/" + name
	out := output{Database: name, DatabaseURL: u.String(), KEKID: KEKID, Tokens: map[string]string{}, CSP: routing.WebCSP}

	_, public, err := secrets.GenerateHPKE()
	if err != nil {
		return out, err
	}
	if err := os.MkdirAll(kekDir, 0o755); err != nil {
		return out, err
	}
	if err := os.WriteFile(filepath.Join(kekDir, KEKID+secrets.PublicSuffix), public, 0o644); err != nil {
		return out, err
	}

	pool, err := store.Open(ctx, out.DatabaseURL)
	if err != nil {
		return out, err
	}
	defer pool.Close()
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		return out, err
	}
	if _, err := store.Migrate(ctx, pool, ms); err != nil {
		return out, err
	}
	s := store.New(pool)
	user, err := s.CreateUser(ctx, "admin")
	if err != nil {
		return out, err
	}
	expires := time.Now().Add(6 * time.Hour)
	for _, scope := range []string{api.ScopeAdmin, api.ScopeDeploy, api.ScopeRead} {
		plain, prefix, hash := api.NewToken()
		if _, err := s.CreateToken(ctx, store.NewToken{UserID: user.ID, Name: "ui-" + scope, Prefix: prefix, Hash: hash,
			Scopes: []string{scope}, ExpiresAt: &expires}); err != nil {
			return out, err
		}
		out.Tokens[scope] = plain
	}

	str := func(s string) *string { return &s }
	port := func(p int) *int { return &p }
	yes := true
	web, err := s.CreateApp(ctx, store.NewApp{OwnerID: user.ID, Slug: "web", RepoFullName: "acme/web",
		AppSettings: store.AppSettings{Branch: str("main"), InternalPort: port(8080), HealthPath: str("/healthz"), AutoDeploy: &yes}})
	if err != nil {
		return out, err
	}
	if _, err := s.CreateApp(ctx, store.NewApp{OwnerID: user.ID, Slug: "docs", RepoFullName: "acme/docs",
		AppSettings: store.AppSettings{Branch: str("release"), InternalPort: port(3000)}}); err != nil {
		return out, err
	}
	// 25 releases of web, as a worker would have left them: 1 active, 1
	// failed, 1 rollback, 22 superseded; older than the first page of 20.
	_, err = pool.Exec(ctx, `
		DO $$
		DECLARE op uuid; dep uuid; first uuid; i int; st text; app uuid := '`+web.ID+`';
		BEGIN
		  FOR i IN 1..25 LOOP
		    st := CASE WHEN i = 25 THEN 'active' WHEN i = 24 THEN 'failed' ELSE 'superseded' END;
		    INSERT INTO operations (app_id, kind, idempotency_key, status, attempt, finished_at, created_at)
		      VALUES (app, CASE WHEN i = 23 THEN 'rollback' ELSE 'deploy' END, 'seed:' || i,
		              CASE WHEN st = 'failed' THEN 'failed' ELSE 'succeeded' END, 1,
		              now() - (26 - i) * interval '1 hour', now() - (26 - i) * interval '1 hour')
		      RETURNING id INTO op;
		    INSERT INTO operation_events (operation_id, seq, level, message)
		      VALUES (op, 1, 'info', 'seeded release ' || i);
		    INSERT INTO deployments (app_id, operation_id, kind, source_deployment_id, source_commit_sha, image_id,
		                             container_id, status, failure_reason, created_at, active_at, ended_at)
		      VALUES (app, op, CASE WHEN i = 23 THEN 'rollback' ELSE 'build' END, CASE WHEN i = 23 THEN first END,
		              encode(sha256(i::text::bytea), 'hex'),
		              CASE WHEN st = 'failed' THEN NULL ELSE 'sha256:' || encode(sha256(('img' || i)::bytea), 'hex') END,
		              CASE WHEN st = 'failed' THEN NULL ELSE encode(sha256(('c' || i)::bytea), 'hex') END,
		              st, CASE WHEN st = 'failed' THEN 'health check: health check did not pass within 1m0s: GET /healthz: 503' END,
		              now() - (26 - i) * interval '1 hour',
		              CASE WHEN st <> 'failed' THEN now() - (26 - i) * interval '1 hour' END,
		              CASE WHEN st = 'superseded' THEN now() - (25 - i) * interval '1 hour' END)
		      RETURNING id INTO dep;
		    IF i = 1 THEN first := dep; END IF;
		  END LOOP;
		END $$`)
	return out, err
}
