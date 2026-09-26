//go:build integration

package store_test

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

func hashOf(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func TestTokens(t *testing.T) {
	s, u, db := newStore(t)
	ctx := t.Context()
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	create := func(prefix string, expires *time.Time) store.Token {
		t.Helper()
		tok, err := s.CreateToken(ctx, store.NewToken{UserID: u.ID, Name: "cli", Prefix: prefix,
			Hash: hashOf(prefix), Scopes: []string{"read", "deploy"}, ExpiresAt: expires})
		if err != nil {
			t.Fatalf("CreateToken %s: %v", prefix, err)
		}
		return tok
	}
	live := create("shp_live0001", &future)
	create("shp_noexpiry", nil)
	// Expiry must be after creation, so an already-expired token is made by
	// moving created_at back first.
	expired := create("shp_expired1", &future)
	if _, err := db.Exec(ctx, `UPDATE api_tokens SET created_at = $2, expires_at = $3 WHERE id = $1`,
		expired.ID, past.Add(-time.Hour), past); err != nil {
		t.Fatal(err)
	}

	t.Run("active lookup", func(t *testing.T) {
		got, err := s.ActiveTokenByHash(ctx, hashOf("shp_live0001"))
		if err != nil || got.ID != live.ID || !reflect.DeepEqual(got.Scopes, []string{"read", "deploy"}) {
			t.Fatalf("ActiveTokenByHash = %+v, %v", got, err)
		}
		if _, err := s.ActiveTokenByHash(ctx, hashOf("shp_noexpiry")); err != nil {
			t.Fatalf("token without expiry: %v", err)
		}
		for _, h := range [][]byte{hashOf("shp_expired1"), hashOf("unknown")} {
			if _, err := s.ActiveTokenByHash(ctx, h); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("inactive token lookup = %v, want ErrNotFound", err)
			}
		}
	})

	t.Run("revoke is idempotent and final", func(t *testing.T) {
		tok := create("shp_revoke01", &future)
		first, err := s.RevokeToken(ctx, tok.Prefix)
		if err != nil || first.RevokedAt == nil {
			t.Fatalf("RevokeToken = %+v, %v", first, err)
		}
		again, err := s.RevokeToken(ctx, tok.Prefix)
		if err != nil || !again.RevokedAt.Equal(*first.RevokedAt) {
			t.Fatalf("second RevokeToken moved revoked_at: %v -> %v (%v)", first.RevokedAt, again.RevokedAt, err)
		}
		if _, err := s.ActiveTokenByHash(ctx, hashOf(tok.Prefix)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("revoked token still active: %v", err)
		}
		if _, err := s.RevokeToken(ctx, "shp_missing0"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("revoke unknown = %v, want ErrNotFound", err)
		}
	})

	t.Run("touch writes at most once a minute", func(t *testing.T) {
		if err := s.TouchToken(ctx, live.ID); err != nil {
			t.Fatal(err)
		}
		first, _ := s.ActiveTokenByHash(ctx, hashOf("shp_live0001"))
		if first.LastUsedAt == nil {
			t.Fatal("last_used_at not set")
		}
		if err := s.TouchToken(ctx, live.ID); err != nil {
			t.Fatal(err)
		}
		second, _ := s.ActiveTokenByHash(ctx, hashOf("shp_live0001"))
		if !second.LastUsedAt.Equal(*first.LastUsedAt) {
			t.Fatalf("second touch within a minute wrote again: %v -> %v", first.LastUsedAt, second.LastUsedAt)
		}
	})

	t.Run("prefix and hash are unique", func(t *testing.T) {
		_, err := s.CreateToken(ctx, store.NewToken{UserID: u.ID, Name: "x", Prefix: "shp_live0001", Hash: hashOf("other"), Scopes: []string{"read"}})
		wantConstraint(t, err, store.ErrConflict, "api_tokens_prefix_key")
		_, err = s.CreateToken(ctx, store.NewToken{UserID: u.ID, Name: "x", Prefix: "shp_other001", Hash: hashOf("shp_live0001"), Scopes: []string{"read"}})
		wantConstraint(t, err, store.ErrConflict, "api_tokens_sha256_hash_key")
		_, err = s.CreateToken(ctx, store.NewToken{UserID: u.ID, Name: "x", Prefix: "shp_noscope1", Hash: hashOf("n"), Scopes: []string{}})
		wantConstraint(t, err, store.ErrInvalid, "api_tokens_scopes_check")
	})

	t.Run("list newest first", func(t *testing.T) {
		list, err := s.ListTokens(ctx)
		if err != nil || len(list) != 4 {
			t.Fatalf("ListTokens = %d tokens, %v", len(list), err)
		}
		for i := 1; i < len(list); i++ {
			if list[i].CreatedAt.After(list[i-1].CreatedAt) {
				t.Fatalf("not newest first: %v", list)
			}
		}
	})
}

func TestAudit(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := t.Context()
	e, err := s.RecordAudit(ctx, store.AuditEvent{Actor: "token:shp_abcd1234", Action: "POST /v1/apps", Target: "/v1/apps", Result: store.AuditSuccess, RequestID: "req-1"})
	if err != nil || e.ID == "" || e.TS.IsZero() {
		t.Fatalf("RecordAudit = %+v, %v", e, err)
	}
	if _, err := s.RecordAudit(ctx, store.AuditEvent{Actor: "system", Action: "boot", Result: store.AuditSuccess}); err != nil {
		t.Fatalf("RecordAudit without target: %v", err)
	}
	_, err = s.RecordAudit(ctx, store.AuditEvent{Actor: "system", Action: "x", Result: "maybe"})
	wantConstraint(t, err, store.ErrInvalid, "audit_events_result_check")

	events, err := s.AuditEvents(ctx, 10)
	if err != nil || len(events) != 2 || events[0].Action != "boot" || !reflect.DeepEqual(events[1], e) {
		t.Fatalf("AuditEvents = %+v, %v", events, err)
	}
}
