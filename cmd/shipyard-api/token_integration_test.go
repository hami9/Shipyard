//go:build integration

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/api"
	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
	"github.com/hami9/shipyard/migrations"
)

func TestTokenCommand(t *testing.T) {
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

	var stdout, stderr bytes.Buffer
	if err := tokenCommand(ctx, s, []string{"create", "--name", "bootstrap", "--scope", "admin", "--ttl", "30d"}, &stdout, &stderr); err != nil {
		t.Fatalf("token create: %v (stderr %s)", err, stderr.String())
	}
	plaintext := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(plaintext, api.TokenPrefix) || strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("stdout must be exactly the token, got %q", stdout.String())
	}
	if strings.Contains(stderr.String(), plaintext) {
		t.Fatal("token printed to stderr too")
	}

	// The bootstrap created the admin user and a token that authenticates.
	tok, err := s.ActiveTokenByHash(ctx, api.HashToken(plaintext))
	if err != nil || tok.Name != "bootstrap" || strings.Join(tok.Scopes, ",") != "admin" {
		t.Fatalf("stored token = %+v, %v", tok, err)
	}
	if _, err := s.UserByName(ctx, "admin"); err != nil {
		t.Fatalf("admin user not created: %v", err)
	}

	// Negative: the plaintext appears nowhere in the database.
	var hits int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM api_tokens t WHERE strpos(row_to_json(t)::text, $1) > 0)
		     + (SELECT count(*) FROM audit_events a WHERE strpos(row_to_json(a)::text, $1) > 0)`,
		plaintext[len(api.TokenPrefix):]).Scan(&hits); err != nil || hits != 0 {
		t.Fatalf("plaintext found in %d rows (err %v)", hits, err)
	}

	stdout.Reset()
	if err := tokenCommand(ctx, s, []string{"list"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if out := stdout.String(); !strings.Contains(out, tok.Prefix) || !strings.Contains(out, "active") || strings.Contains(out, plaintext) {
		t.Fatalf("list output:\n%s", out)
	}

	if err := tokenCommand(ctx, s, []string{"revoke", tok.Prefix}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveTokenByHash(ctx, api.HashToken(plaintext)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked token still authenticates: %v", err)
	}
	if err := tokenCommand(ctx, s, []string{"revoke", "shp_unknown1"}, &stdout, &stderr); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoke unknown = %v, want ErrNotFound", err)
	}

	events, err := s.AuditEvents(ctx, 10)
	if err != nil || len(events) != 2 || events[0].Action != "token.revoke" || events[1].Action != "token.create" ||
		events[1].Actor != cliActor || events[1].Target != tok.Prefix {
		t.Fatalf("audit events = %+v, %v", events, err)
	}

	// A second create reuses the existing user.
	stdout.Reset()
	if err := tokenCommand(ctx, s, []string{"create", "--scope", "read"}, &stdout, &stderr); err != nil {
		t.Fatalf("second create: %v", err)
	}
}
