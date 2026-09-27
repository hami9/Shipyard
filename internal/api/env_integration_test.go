//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestEnvEndpoints(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	if r.status != http.StatusCreated {
		t.Fatalf("setup: %d %s", r.status, r.raw)
	}
	const secretValue = "postgres://u:canary-3f9a@db/app"

	if r := f.call("GET", "/v1/apps/web/env", f.reader, "", ""); r.status != 200 || r.raw != `{"revision":0,"vars":[]}`+"\n" {
		t.Fatalf("empty env: %d %s", r.status, r.raw)
	}
	body, _ := json.Marshal(map[string]any{"value": secretValue})
	r = f.json("PUT", "/v1/apps/web/env/DATABASE_URL", string(body))
	if r.status != 200 || r.raw != `{"revision":1,"vars":[{"key":"DATABASE_URL","secret":true}]}`+"\n" {
		t.Fatalf("set secret: %d %s", r.status, r.raw)
	}
	r = f.json("PUT", "/v1/apps/web/env/LOG_LEVEL", `{"value":"info","secret":false}`)
	if r.status != 200 || r.body["revision"] != float64(2) {
		t.Fatalf("set plain: %d %s", r.status, r.raw)
	}
	r = f.call("GET", "/v1/apps/web/env", f.reader, "", "")
	if r.raw != `{"revision":2,"vars":[{"key":"DATABASE_URL","secret":true},{"key":"LOG_LEVEL","secret":false}]}`+"\n" {
		t.Fatalf("list: %s", r.raw)
	}

	// The worker-side Resolve sees the value the API sealed.
	app, _ := f.s.AppBySlug(ctx, "web")
	rev, err := f.s.LatestEnvRevision(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.env.Resolve(ctx, rev.ID); err != nil || got["DATABASE_URL"] != secretValue || got["LOG_LEVEL"] != "info" {
		t.Fatalf("Resolve = %v, %v", got, err)
	}

	if r := f.call("DELETE", "/v1/apps/web/env/LOG_LEVEL", f.admin, "", ""); r.status != 200 || r.body["revision"] != float64(3) {
		t.Fatalf("unset: %d %s", r.status, r.raw)
	}

	tests := []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"unset missing key", "DELETE", "/v1/apps/web/env/LOG_LEVEL", f.admin, "", 404},
		{"bad key", "PUT", "/v1/apps/web/env/BAD-KEY", f.admin, `{"value":"x"}`, 422},
		{"key with digit first", "PUT", "/v1/apps/web/env/1X", f.admin, `{"value":"x"}`, 422},
		{"missing value", "PUT", "/v1/apps/web/env/K", f.admin, `{"secret":true}`, 422},
		{"NUL value", "PUT", "/v1/apps/web/env/K", f.admin, `{"value":"a\u0000b"}`, 422},
		{"value too large", "PUT", "/v1/apps/web/env/K", f.admin, `{"value":"` + strings.Repeat("x", 64<<10+1) + `"}`, 422},
		{"unknown app", "PUT", "/v1/apps/nope/env/K", f.admin, `{"value":"x"}`, 404},
		{"read token", "PUT", "/v1/apps/web/env/K", f.reader, `{"value":"x"}`, 403},
		{"read token lists keys", "GET", "/v1/apps/web/env", f.reader, "", 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctype := ""
			if tt.body != "" {
				ctype = "application/json"
			}
			if r := f.call(tt.method, tt.path, tt.token, ctype, tt.body); r.status != tt.want {
				t.Fatalf("status %d, want %d: %s", r.status, tt.want, r.raw)
			}
		})
	}

	// Negative: the secret never appears in responses, logs, or audit rows.
	if strings.Contains(f.logs.String(), "canary-3f9a") {
		t.Fatal("secret value logged")
	}
	events, _ := f.s.AuditEvents(ctx, 50)
	for _, e := range events {
		if strings.Contains(e.Target+e.Action, "canary") {
			t.Fatalf("secret in audit event %+v", e)
		}
	}
	if len(events) == 0 || events[len(events)-1].Action != "POST /v1/apps" {
		t.Fatalf("audit trail: %+v", events)
	}
}
