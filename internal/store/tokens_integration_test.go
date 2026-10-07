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

// ADR-0011: a rotation inserts the new token and ends the old one in one
// transaction, and does nothing for a token that is no longer active.
func TestRotateToken(t *testing.T) {
	s, u, _ := newStore(t)
	ctx := t.Context()
	in := func(d time.Duration) *time.Time { x := time.Now().Add(d); return &x }
	newToken := func(prefix string, expires *time.Time) store.NewToken {
		return store.NewToken{UserID: u.ID, Name: "ci", Prefix: prefix, Hash: hashOf(prefix), Scopes: []string{"deploy"}, ExpiresAt: expires}
	}
	count := func() int {
		list, err := s.ListTokens(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}

	// No grace: the old token is revoked at once.
	a, _ := s.CreateToken(ctx, newToken("shp_rotA0001", in(48*time.Hour)))
	old, created, err := s.RotateToken(ctx, a.ID, newToken("shp_rotA0002", in(48*time.Hour)), 0)
	if err != nil || old.RevokedAt == nil || created.Prefix != "shp_rotA0002" || created.Name != "ci" {
		t.Fatalf("rotate without grace = %+v, %+v, %v", old, created, err)
	}
	if _, err := s.ActiveTokenByHash(ctx, hashOf("shp_rotA0001")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old token still active: %v", err)
	}

	// Grace: the old token keeps working until then, never past its own expiry.
	b, _ := s.CreateToken(ctx, newToken("shp_rotB0001", in(48*time.Hour)))
	old, _, err = s.RotateToken(ctx, b.ID, newToken("shp_rotB0002", in(48*time.Hour)), time.Hour)
	if err != nil || old.RevokedAt != nil || old.ExpiresAt == nil || time.Until(*old.ExpiresAt) < 59*time.Minute || time.Until(*old.ExpiresAt) > 61*time.Minute {
		t.Fatalf("rotate with grace = %+v, %v (want an expiry in 1h)", old, err)
	}
	if _, err := s.ActiveTokenByHash(ctx, hashOf("shp_rotB0001")); err != nil {
		t.Fatalf("old token stopped during its grace: %v", err)
	}
	c, _ := s.CreateToken(ctx, newToken("shp_rotC0001", in(time.Hour)))
	old, _, err = s.RotateToken(ctx, c.ID, newToken("shp_rotC0002", in(48*time.Hour)), 24*time.Hour)
	if err != nil || !old.ExpiresAt.Equal(*c.ExpiresAt) {
		t.Fatalf("grace past the old expiry = %+v, %v (want expiry %v kept)", old, err, c.ExpiresAt)
	}
	d, _ := s.CreateToken(ctx, newToken("shp_rotD0001", nil))
	if old, _, err = s.RotateToken(ctx, d.ID, newToken("shp_rotD0002", in(48*time.Hour)), time.Hour); err != nil || old.ExpiresAt == nil {
		t.Fatalf("grace on a token without expiry = %+v, %v", old, err)
	}

	// Negative: a revoked token cannot be rotated, and no new token is left behind.
	before := count()
	if _, _, err := s.RotateToken(ctx, a.ID, newToken("shp_rotA0003", in(48*time.Hour)), 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rotate a revoked token = %v, want ErrNotFound", err)
	}
	if count() != before {
		t.Fatal("a failed rotation inserted a token")
	}
	got, err := s.TokenByPrefix(ctx, "shp_rotA0001")
	if err != nil || got.ID != a.ID || got.RevokedAt == nil {
		t.Fatalf("TokenByPrefix = %+v, %v", got, err)
	}
	if _, err := s.TokenByPrefix(ctx, "shp_nothere1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("TokenByPrefix unknown = %v", err)
	}
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
