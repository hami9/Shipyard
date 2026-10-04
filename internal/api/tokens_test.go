package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// fakeTokenAdmin keeps tokens by prefix.
type fakeTokenAdmin struct {
	byPrefix map[string]store.Token
	rotated  []store.NewToken
	graces   []time.Duration
	rotErr   error
}

func (f *fakeTokenAdmin) ListTokens(context.Context) ([]store.Token, error) {
	var out []store.Token
	for _, t := range f.byPrefix {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeTokenAdmin) RevokeToken(_ context.Context, prefix string) (store.Token, error) {
	t, ok := f.byPrefix[prefix]
	if !ok {
		return t, store.ErrNotFound
	}
	now := time.Now()
	t.RevokedAt = &now
	f.byPrefix[prefix] = t
	return t, nil
}

func (f *fakeTokenAdmin) RotateToken(_ context.Context, oldID string, n store.NewToken, grace time.Duration) (store.Token, store.Token, error) {
	if f.rotErr != nil {
		return store.Token{}, store.Token{}, f.rotErr
	}
	f.rotated, f.graces = append(f.rotated, n), append(f.graces, grace)
	old := store.Token{ID: oldID, Prefix: "shp_oldtoken"}
	return old, store.Token{Prefix: n.Prefix, Name: n.Name, Scopes: n.Scopes, ExpiresAt: n.ExpiresAt}, nil
}

func tokenHandler(t *testing.T) (http.Handler, *fakeTokens, *fakeTokenAdmin) {
	t.Helper()
	tokens := newFakeTokens()
	admin := &fakeTokenAdmin{byPrefix: map[string]store.Token{"shp_listed01": {Prefix: "shp_listed01", Name: "ci", Scopes: []string{ScopeDeploy}}}}
	h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{Tokens: tokens, TokenAdmin: admin, Audit: &fakeAudit{}})
	return h, tokens, admin
}

func call(h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTokenRoutesNeedAdmin(t *testing.T) {
	h, tokens, _ := tokenHandler(t)
	deploy, _ := tokens.add(ScopeDeploy)
	admin, _ := tokens.add(ScopeAdmin)
	if rec := call(h, "GET", "/v1/tokens", deploy, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("list with deploy = %d, want 403", rec.Code)
	}
	if rec := call(h, "DELETE", "/v1/tokens/shp_listed01", deploy, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("revoke with deploy = %d, want 403", rec.Code)
	}
	rec := call(h, "GET", "/v1/tokens", admin, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"prefix":"shp_listed01"`) ||
		!strings.Contains(rec.Body.String(), `"status":"active"`) || strings.Contains(rec.Body.String(), "hash") {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	rec = call(h, "DELETE", "/v1/tokens/shp_listed01", admin, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"revoked"`) {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body)
	}
	for _, prefix := range []string{"shp_nothere1", "not-a-prefix", "shp_" + strings.Repeat("x", 17)} {
		if rec := call(h, "DELETE", "/v1/tokens/"+prefix, admin, ""); rec.Code != http.StatusNotFound {
			t.Errorf("revoke %s = %d, want 404", prefix, rec.Code)
		}
	}
}

func TestRotateSelf(t *testing.T) {
	h, tokens, admin := tokenHandler(t)
	read, cur := tokens.add(ScopeRead) // any scope may rotate itself

	rec := call(h, "POST", "/v1/tokens/self/rotate", read, "")
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotate = %d, Cache-Control %q: %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	var got rotateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !wellFormedToken(got.Token) || got.New.Prefix != got.Token[:tokenDisplayLen] {
		t.Fatalf("rotate body = %+v, %v", got, err)
	}
	n := admin.rotated[0]
	if n.Name != cur.Name || strings.Join(n.Scopes, ",") != ScopeRead || string(n.Hash) != string(HashToken(got.Token)) {
		t.Fatalf("new token = %+v, want the old name and scopes and the returned token's hash", n)
	}
	// No expiry on the old token: the default lifetime; no grace: ends now.
	if life := time.Until(*n.ExpiresAt); life < DefaultTokenTTL-time.Minute || life > DefaultTokenTTL {
		t.Fatalf("new lifetime = %v", life)
	}
	if admin.graces[0] != 0 {
		t.Fatalf("grace = %v, want none", admin.graces[0])
	}

	rec = call(h, "POST", "/v1/tokens/self/rotate", read, `{"grace_seconds": 3600}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rotate with grace = %d %s", rec.Code, rec.Body)
	}
	if admin.graces[1] != time.Hour {
		t.Fatalf("grace = %v, want 1h", admin.graces[1])
	}

	// Negative: bad bodies, out-of-range grace, and a token gone meanwhile.
	for body, want := range map[string]int{
		`{"grace_seconds": 604801}`: http.StatusUnprocessableEntity,
		`{"grace_seconds": -1}`:     http.StatusUnprocessableEntity,
		`{"grace": 5}`:              http.StatusBadRequest,
		`not json`:                  http.StatusBadRequest,
	} {
		if rec := call(h, "POST", "/v1/tokens/self/rotate", read, body); rec.Code != want {
			t.Errorf("rotate %s = %d, want %d", body, rec.Code, want)
		}
	}
	admin.rotErr = store.ErrNotFound
	if rec := call(h, "POST", "/v1/tokens/self/rotate", read, ""); rec.Code != http.StatusConflict {
		t.Fatalf("rotate a token revoked meanwhile = %d, want 409", rec.Code)
	}
	if rec := call(h, "POST", "/v1/tokens/self/rotate", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("rotate without a token = %d, want 401", rec.Code)
	}
}

func TestRotationKeepsLifetime(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := created.Add(100 * 24 * time.Hour)
	for _, tc := range []struct {
		life, want time.Duration
	}{
		{30 * 24 * time.Hour, 30 * 24 * time.Hour},
		{time.Minute, MinTokenTTL},
		{400 * 24 * time.Hour, MaxTokenTTL},
	} {
		exp := created.Add(tc.life)
		_, n := Rotation(store.Token{UserID: "u", Name: "n", Scopes: []string{ScopeAdmin}, CreatedAt: created, ExpiresAt: &exp}, now)
		if got := n.ExpiresAt.Sub(now); got != tc.want || n.UserID != "u" {
			t.Errorf("lifetime %v: new token lives %v, want %v", tc.life, got, tc.want)
		}
	}
}
