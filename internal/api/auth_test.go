package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hami9/shipyard/internal/logging"
	"github.com/hami9/shipyard/internal/store"
)

type fakeTokens struct {
	mu      sync.Mutex
	byHash  map[string]store.Token
	lookups int
	touched []string
	err     error
}

func newFakeTokens() *fakeTokens { return &fakeTokens{byHash: map[string]store.Token{}} }

// add registers a new token with the given scopes and returns its plaintext.
func (f *fakeTokens) add(scopes ...string) (string, store.Token) {
	plain, prefix, hash := NewToken()
	tok := store.Token{ID: "id-" + prefix, Name: "test", Prefix: prefix, Scopes: scopes}
	f.byHash[string(hash)] = tok
	return plain, tok
}

func (f *fakeTokens) ActiveTokenByHash(_ context.Context, hash []byte) (store.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.err != nil {
		return store.Token{}, f.err
	}
	t, ok := f.byHash[string(hash)]
	if !ok {
		return store.Token{}, store.ErrNotFound
	}
	return t, nil
}

func (f *fakeTokens) TouchToken(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, id)
	return nil
}

type fakeAudit struct {
	mu     sync.Mutex
	events []store.AuditEvent
	err    error
}

func (f *fakeAudit) RecordAudit(ctx context.Context, e store.AuditEvent) (store.AuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return e, ctx.Err()
	}
	if f.err != nil {
		return e, f.err
	}
	f.events = append(f.events, e)
	return e, nil
}

type authFixture struct {
	h      http.Handler
	tokens *fakeTokens
	audit  *fakeAudit
	logs   *bytes.Buffer
}

// newAuthFixture serves /v1/whoami plus a test mutation route whose status
// the request chooses, so audit results can be exercised before real
// mutation endpoints exist.
func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{tokens: newFakeTokens(), audit: &fakeAudit{}, logs: &bytes.Buffer{}}
	log := logging.New(f.logs, slog.LevelDebug, logging.FormatJSON)
	a := &authenticator{log: log, tokens: f.tokens, audit: f.audit}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/whoami", a.protect(ScopeRead, http.HandlerFunc(handleWhoami)))
	mux.Handle("POST /v1/things/{id}", a.protect(ScopeDeploy, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fail") != "" {
			writeProblem(w, http.StatusConflict, "nope")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})))
	f.h = withRequestID(withAccessLog(log, mux))
	return f
}

func (f *authFixture) do(method, target, authorization string) *httptest.ResponseRecorder {
	h := http.Header{}
	if authorization != "" {
		h.Set("Authorization", authorization)
	}
	h.Set(headerRequestID, "req-42")
	return do(f.h, method, target, h)
}

func TestAuthRejects(t *testing.T) {
	f := newAuthFixture(t)
	unknown, _, _ := NewToken()
	tests := []struct {
		name, auth, wantChallenge string
		wantLookup                bool
	}{
		{"missing header", "", realm, false},
		{"other scheme", "Basic dXNlcjpwYXNz", realm, false},
		{"bare token", unknown, realm, false},
		{"malformed token", "Bearer shp_short", realm + `, error="invalid_token"`, false},
		{"foreign token", "Bearer ghp_" + strings.Repeat("a", 43), realm + `, error="invalid_token"`, false},
		{"unknown token", "Bearer " + unknown, realm + `, error="invalid_token"`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := f.tokens.lookups
			rec := f.do(http.MethodGet, "/v1/whoami", tt.auth)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != tt.wantChallenge {
				t.Errorf("WWW-Authenticate = %q, want %q", got, tt.wantChallenge)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("Content-Type = %q", ct)
			}
			if looked := f.tokens.lookups > before; looked != tt.wantLookup {
				t.Errorf("database lookup = %v, want %v", looked, tt.wantLookup)
			}
		})
	}
	if len(f.audit.events) != 0 {
		t.Fatalf("anonymous requests were audited: %+v", f.audit.events)
	}
}

func TestWhoami(t *testing.T) {
	f := newAuthFixture(t)
	plain, tok := f.tokens.add(ScopeRead)
	for _, scheme := range []string{"Bearer ", "bearer ", "BEARER  "} {
		rec := f.do(http.MethodGet, "/v1/whoami", scheme+plain)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: status = %d, body %s", scheme, rec.Code, rec.Body)
		}
		var body struct {
			Token  string   `json:"token"`
			Scopes []string `json:"scopes"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Token != tok.Prefix || len(body.Scopes) != 1 {
			t.Fatalf("body = %s (%v)", rec.Body, err)
		}
		if strings.Contains(rec.Body.String(), plain) {
			t.Fatal("response echoes the token")
		}
	}
	if len(f.tokens.touched) != 3 || f.tokens.touched[0] != tok.ID {
		t.Fatalf("touched = %v", f.tokens.touched)
	}
	if len(f.audit.events) != 0 {
		t.Fatalf("reads were audited: %+v", f.audit.events)
	}
	// Negative: the plaintext never reaches the logs.
	if strings.Contains(f.logs.String(), plain) || strings.Contains(f.logs.String(), plain[len(TokenPrefix):]) {
		t.Fatal("token plaintext logged")
	}
}

func TestMutationScopeAndAudit(t *testing.T) {
	f := newAuthFixture(t)
	reader, rtok := f.tokens.add(ScopeRead)
	deployer, dtok := f.tokens.add(ScopeDeploy)

	rec := f.do(http.MethodPost, "/v1/things/7", "Bearer "+reader)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read token on deploy route: status %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != realm+`, error="insufficient_scope", scope="deploy"` {
		t.Errorf("WWW-Authenticate = %q", got)
	}
	if rec := f.do(http.MethodPost, "/v1/things/7", "Bearer "+deployer); rec.Code != http.StatusAccepted {
		t.Fatalf("deploy token: status %d", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/v1/things/8?fail=1", "Bearer "+deployer); rec.Code != http.StatusConflict {
		t.Fatalf("failing handler: status %d", rec.Code)
	}

	want := []store.AuditEvent{
		{Actor: "token:" + rtok.Prefix, Action: "POST /v1/things/{id}", Target: "/v1/things/7", Result: store.AuditDenied, RequestID: "req-42"},
		{Actor: "token:" + dtok.Prefix, Action: "POST /v1/things/{id}", Target: "/v1/things/7", Result: store.AuditSuccess, RequestID: "req-42"},
		{Actor: "token:" + dtok.Prefix, Action: "POST /v1/things/{id}", Target: "/v1/things/8", Result: store.AuditFailure, RequestID: "req-42"},
	}
	if len(f.audit.events) != len(want) {
		t.Fatalf("audit events = %+v", f.audit.events)
	}
	for i := range want {
		if f.audit.events[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, f.audit.events[i], want[i])
		}
	}
}

func TestAuditSurvivesClientCancel(t *testing.T) {
	f := newAuthFixture(t)
	admin, _ := f.tokens.add(ScopeAdmin)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/things/1", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	cancel() // the client is gone before the audit write
	f.h.ServeHTTP(httptest.NewRecorder(), req)
	if len(f.audit.events) != 1 {
		t.Fatalf("audit event lost on client cancel: %+v", f.audit.events)
	}
}

func TestAuthDependencyFailures(t *testing.T) {
	f := newAuthFixture(t)
	admin, _ := f.tokens.add(ScopeAdmin)

	f.audit.err = errors.New("db down")
	if rec := f.do(http.MethodPost, "/v1/things/1", "Bearer "+admin); rec.Code != http.StatusAccepted {
		t.Fatalf("audit failure changed the response: %d", rec.Code)
	}
	if !strings.Contains(f.logs.String(), "audit event not recorded") {
		t.Fatal("audit failure not logged")
	}

	f.tokens.err = errors.New("connection refused 10.0.0.9")
	rec := f.do(http.MethodGet, "/v1/whoami", "Bearer "+admin)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "10.0.0.9") {
		t.Fatalf("token store down: status %d, body %s", rec.Code, rec.Body)
	}
}
