//go:build integration

package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// deployment creates a deployment of app through the queue, as the worker
// would: claim, then create (status building).
func (f *apiFixture) deployment(appID, key string) store.Deployment {
	f.t.Helper()
	ctx := f.t.Context()
	if _, err := f.s.EnqueueOperation(ctx, store.NewOperation{AppID: appID, Kind: "deploy", IdempotencyKey: key}); err != nil {
		f.t.Fatal(err)
	}
	op, err := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if err != nil {
		f.t.Fatal(err)
	}
	d, err := f.s.CreateDeployment(ctx, "w1", store.NewDeployment{OperationID: op.ID, SourceCommitSHA: strings.Repeat("ab", 20)})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.s.FailDeployment(ctx, d.ID, "w1", "build failed: missing-file"); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.s.FailOperation(ctx, op.ID, "w1", "build failed", 0); err != nil {
		f.t.Fatal(err)
	}
	return d
}

// P3.1: GET /v1/apps/{app}/deployments pages through the history.
func TestReleasesEndpoint(t *testing.T) {
	f := newAPIFixture(t)
	r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	if r.status != 201 {
		t.Fatalf("setup: %d %s", r.status, r.raw)
	}
	appID := r.body["id"].(string)
	if r := f.call("GET", "/v1/apps/web/deployments", f.reader, "", ""); r.status != 200 || r.raw != `{"deployments":[]}`+"\n" {
		t.Fatalf("empty: %d %q", r.status, r.raw)
	}
	var ids []string
	for _, k := range []string{"a", "b", "c"} {
		ids = append(ids, f.deployment(appID, k).ID)
	}

	r = f.call("GET", "/v1/apps/web/deployments?limit=2", f.reader, "", "")
	deps, _ := r.body["deployments"].([]any)
	if r.status != 200 || len(deps) != 2 || r.body["next"] != ids[1] {
		t.Fatalf("page 1: %d %s", r.status, r.raw)
	}
	first := deps[0].(map[string]any)
	if first["id"] != ids[2] || first["status"] != "failed" || first["commit"] != strings.Repeat("ab", 20) ||
		first["failure_reason"] != "build failed: missing-file" || first["env_revision"] != float64(0) || first["kind"] != "build" {
		t.Fatalf("newest = %v", first)
	}
	r = f.call("GET", "/v1/apps/"+appID+"/deployments?limit=2&before="+ids[1], f.reader, "", "")
	deps, _ = r.body["deployments"].([]any)
	if r.status != 200 || len(deps) != 1 || deps[0].(map[string]any)["id"] != ids[0] || r.body["next"] != nil {
		t.Fatalf("page 2: %d %s", r.status, r.raw)
	}

	for _, tt := range []struct {
		name, path, token string
		want              int
	}{
		{"limit 0", "/v1/apps/web/deployments?limit=0", f.reader, 422},
		{"limit too big", "/v1/apps/web/deployments?limit=101", f.reader, 422},
		{"limit not a number", "/v1/apps/web/deployments?limit=ten", f.reader, 422},
		{"before not an id", "/v1/apps/web/deployments?before=x", f.reader, 422},
		{"before unknown", "/v1/apps/web/deployments?before=00000000-0000-4000-8000-000000000000", f.reader, 422},
		{"unknown app", "/v1/apps/nope/deployments", f.reader, 404},
		{"no token", "/v1/apps/web/deployments", "", 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if r := f.call("GET", tt.path, tt.token, "", ""); r.status != tt.want || r.header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("%d, want %d: %s", r.status, tt.want, r.raw)
			}
		})
	}
	if r := f.call("GET", "/v1/apps/web/deployments?before=x&limit=0", f.reader, "", ""); r.status != http.StatusUnprocessableEntity || len(fieldsOf(r)) != 2 {
		t.Fatalf("both fields reported: %s", r.raw)
	}
}
