//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// served walks a deployment of app through to active, as the worker would,
// pinned to revision rev (nil: none).
func (f *apiFixture) served(appID string, rev *string) store.Deployment {
	f.t.Helper()
	ctx := f.t.Context()
	if _, err := f.s.EnqueueOperation(ctx, store.NewOperation{AppID: appID, Kind: "deploy", IdempotencyKey: "k-" + time.Now().Format(time.RFC3339Nano)}); err != nil {
		f.t.Fatal(err)
	}
	op, err := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if err != nil {
		f.t.Fatal(err)
	}
	d, err := f.s.CreateDeployment(ctx, "w1", store.NewDeployment{OperationID: op.ID, SourceCommitSHA: strings.Repeat("ab", 20), EnvRevisionID: rev})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, err := range []error{
		f.s.RecordImage(ctx, d.ID, "w1", "sha256:"+strings.Repeat("0f", 32), json.RawMessage(`{}`)),
		f.s.RecordContainer(ctx, d.ID, "w1", strings.Repeat("c1", 32)),
		f.s.MarkHealthChecking(ctx, d.ID, "w1"),
	} {
		if err != nil {
			f.t.Fatal(err)
		}
	}
	if _, err := f.s.ActivateDeployment(ctx, d.ID, "w1", "", nil); err != nil {
		f.t.Fatal(err)
	}
	return d
}

// P3.3: POST /v1/apps/{app}/rollbacks queues a rollback to a deployment
// that served before; changed secrets need an explicit choice.
func TestRollbackEndpoint(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	appID := r.body["id"].(string)
	otherID := f.json("POST", "/v1/apps", `{"slug":"api","repo":"hami9/api","branch":"main","port":3000}`).body["id"].(string)
	rev1, err := f.env.Set(ctx, appID, "DB_URL", []byte("postgres://old"), true)
	if err != nil {
		t.Fatal(err)
	}
	old := f.served(appID, &rev1.ID)
	current := f.served(appID, &rev1.ID) // supersedes old
	failed := f.deployment(appID, "failed-one")
	foreign := f.served(otherID, nil)
	post := func(token, body, key string) response {
		t.Helper()
		return f.callKey("POST", "/v1/apps/web/rollbacks", token, body, key)
	}

	// Unchanged secrets: queued at once, with the old configuration.
	r = post(f.deployer, `{"to":"`+old.ID+`"}`, "rb-1")
	if r.status != 202 || opField(r, "kind") != "rollback" || opField(r, "status") != "queued" {
		t.Fatalf("rollback: %d %s", r.status, r.raw)
	}
	var p map[string]any
	json.Unmarshal([]byte(mustJSON(opField(r, "payload"))), &p)
	if p["target"] != old.ID || p["with_current_config"] != nil {
		t.Fatalf("payload = %v", p)
	}
	if r := post(f.deployer, `{"to":"`+old.ID+`"}`, "rb-1"); r.status != 200 || r.body["created"] != false {
		t.Fatalf("replay: %d %s", r.status, r.raw)
	}

	// A rotated secret needs a choice; the keys are named, never values.
	if _, err := f.env.Set(ctx, appID, "DB_URL", []byte("postgres://new"), true); err != nil {
		t.Fatal(err)
	}
	r = post(f.deployer, `{"to":"`+old.ID+`"}`, "rb-2")
	if r.status != 409 || !strings.Contains(r.raw, "DB_URL") || strings.Contains(r.raw, "postgres://") || !strings.Contains(r.raw, "with_current_config") {
		t.Fatalf("rotated: %d %s", r.status, r.raw)
	}
	for _, flag := range []string{"with_old_config", "with_current_config"} {
		r = post(f.deployer, `{"to":"`+old.ID+`","`+flag+`":true}`, "rb-"+flag)
		if r.status != 202 {
			t.Fatalf("%s: %d %s", flag, r.status, r.raw)
		}
	}
	json.Unmarshal([]byte(mustJSON(opField(r, "payload"))), &p)
	if p["with_current_config"] != true {
		t.Fatalf("current config payload = %v", p)
	}

	for _, tt := range []struct {
		name, token, body string
		want              int
	}{
		{"active target", f.deployer, `{"to":"` + current.ID + `"}`, 409},
		{"failed target", f.deployer, `{"to":"` + failed.ID + `"}`, 422},
		{"other app's", f.deployer, `{"to":"` + foreign.ID + `"}`, 422},
		{"unknown", f.deployer, `{"to":"00000000-0000-4000-8000-000000000000"}`, 422},
		{"no target", f.deployer, `{}`, 422},
		{"both configs", f.deployer, `{"to":"` + old.ID + `","with_old_config":true,"with_current_config":true}`, 422},
		{"unknown field", f.deployer, `{"to":"` + old.ID + `","force":true}`, 400},
		{"read token", f.reader, `{"to":"` + old.ID + `"}`, 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if r := post(tt.token, tt.body, ""); r.status != tt.want {
				t.Fatalf("%d, want %d: %s", r.status, tt.want, r.raw)
			}
		})
	}
}

// callKey sends a JSON request with an Idempotency-Key (empty: none).
func (f *apiFixture) callKey(method, path, token, body, key string) response {
	f.t.Helper()
	req, _ := http.NewRequestWithContext(f.t.Context(), method, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	return readResponse(res)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
