package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/applogs"
)

type fakeWorker struct {
	st  applogs.Status
	err error
}

func (f fakeWorker) Status(ctx context.Context) (applogs.Status, error) {
	if _, ok := ctx.Deadline(); !ok {
		return applogs.Status{}, errors.New("asked without a deadline")
	}
	return f.st, f.err
}

func statusHandler(t *testing.T, w WorkerStatus) (http.Handler, string) {
	t.Helper()
	tokens := newFakeTokens()
	read, _ := tokens.add(ScopeRead)
	h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{Tokens: tokens, Audit: &fakeAudit{}, Worker: w,
		DomainPolicy: DomainPolicy{PublicIPs: []netip.Addr{netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("2001:db8::7")}}})
	return conform(t, h), read
}

// The worker's answer, its absence, and the public IPs, for a read token.
func TestStatus(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name   string
		worker WorkerStatus
		want   string
	}{
		{"up", fakeWorker{st: applogs.Status{CheckedAt: at, Apps: []applogs.AppHealth{{App: "web", Running: true, Healthy: true}}}},
			`{"worker":"up","checked_at":"2026-10-07T12:00:00Z","apps":[{"app":"web","running":true,"healthy":true}],"public_ips":["203.0.113.7","2001:db8::7"]}`},
		{"up before its first check", fakeWorker{st: applogs.Status{}},
			`{"worker":"up","checked_at":null,"apps":[],"public_ips":["203.0.113.7","2001:db8::7"]}`},
		{"down", fakeWorker{err: applogs.ErrUnavailable},
			`{"worker":"down","checked_at":null,"apps":[],"public_ips":["203.0.113.7","2001:db8::7"]}`},
		{"not configured", nil,
			`{"worker":"down","checked_at":null,"apps":[],"public_ips":["203.0.113.7","2001:db8::7"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, read := statusHandler(t, tt.worker)
			rec := call(h, http.MethodGet, "/v1/status", read, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if g := mustCanon(t, rec.Body.String()); g != mustCanon(t, tt.want) {
				t.Fatalf("got %s\nwant %s", g, tt.want)
			}
		})
	}
	// Without a token, nothing.
	h, _ := statusHandler(t, nil)
	if rec := call(h, http.MethodGet, "/v1/status", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
}

func mustCanon(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
