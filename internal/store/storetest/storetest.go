// Package storetest gives integration tests an isolated PostgreSQL database.
//
// Tests read the admin connection URL from SHIPYARD_TEST_DATABASE_URL (URL
// form, with a role allowed to CREATE DATABASE). Each call creates a fresh
// database and drops it when the test ends, so tests never share state.
package storetest

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// EnvURL names the variable holding the admin connection URL.
const EnvURL = "SHIPYARD_TEST_DATABASE_URL"

// NewDatabase creates an empty database and returns its connection URL.
// It fails the test when SHIPYARD_TEST_DATABASE_URL is unset: integration
// tests that silently skip would report a false green.
func NewDatabase(t testing.TB) string {
	t.Helper()
	admin := os.Getenv(EnvURL)
	if admin == "" {
		t.Fatalf("%s is not set; run `make dev-up` and use `make test-integration`", EnvURL)
	}
	u, err := url.Parse(admin)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatalf("%s must be a postgres:// URL", EnvURL)
	}

	name := "shipyard_test_" + strings.ToLower(rand.Text()[:12])
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect to %s: %v", EnvURL, err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Errorf("connect for cleanup: %v", err)
			return
		}
		defer conn.Close(ctx)
		// WITH (FORCE) terminates connections a test left open (PostgreSQL 13+).
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{name}.Sanitize())); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	u.Path = "/" + name
	return u.String()
}
