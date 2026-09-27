//go:build integration

package api

import (
	"net/http"
	"strings"
	"testing"
)

func (f *apiFixture) deploy(app, token, key, body string) response {
	f.t.Helper()
	req, _ := http.NewRequest("POST", f.srv.URL+"/v1/apps/"+app+"/deployments", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
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

func opField(r response, field string) any {
	op, _ := r.body["operation"].(map[string]any)
	return op[field]
}

func TestDeployEndpoint(t *testing.T) {
	f := newAPIFixture(t)
	for _, slug := range []string{"web", "api"} {
		if r := f.json("POST", "/v1/apps", `{"slug":"`+slug+`","repo":"hami9/demo","branch":"main","port":3000}`); r.status != 201 {
			t.Fatalf("setup %s: %d %s", slug, r.status, r.raw)
		}
	}
	sha := strings.Repeat("ab", 20)

	first := f.deploy("web", f.deployer, "ci-run-1", `{"ref":"`+sha+`"}`)
	if first.status != http.StatusAccepted || first.body["created"] != true || opField(first, "status") != "queued" ||
		opField(first, "idempotency_key") != "api:ci-run-1" || first.header.Get("Location") != "/v1/operations/"+opField(first, "id").(string) {
		t.Fatalf("deploy: %d %s", first.status, first.raw)
	}
	id := opField(first, "id").(string)

	replay := f.deploy("web", f.deployer, "ci-run-1", `{"ref":"`+sha+`"}`)
	if replay.status != http.StatusOK || replay.body["created"] != false || opField(replay, "id") != id {
		t.Fatalf("replay: %d %s", replay.status, replay.raw)
	}
	if r := f.deploy("web", f.deployer, "ci-run-1", `{"ref":"`+strings.Repeat("cd", 20)+`"}`); r.status != http.StatusConflict {
		t.Fatalf("same key, other ref: %d %s", r.status, r.raw)
	}
	if r := f.deploy("api", f.deployer, "ci-run-1", ""); r.status != http.StatusConflict {
		t.Fatalf("same key, other app: %d %s", r.status, r.raw)
	}

	// No key: one is generated. The newer request supersedes the queued one.
	latest := f.deploy("web", f.admin, "", "")
	if latest.status != http.StatusAccepted || !strings.HasPrefix(opField(latest, "idempotency_key").(string), "api:auto:") {
		t.Fatalf("keyless deploy: %d %s", latest.status, latest.raw)
	}
	if sup, _ := latest.body["superseded"].([]any); len(sup) != 1 || sup[0] != id {
		t.Fatalf("superseded = %v, want [%s]", latest.body["superseded"], id)
	}
	if r := f.call("GET", "/v1/operations/"+id, f.reader, "", ""); r.status != 200 || r.body["status"] != "cancelled" ||
		!strings.HasPrefix(r.body["last_error"].(string), "superseded by") {
		t.Fatalf("GET superseded op: %d %s", r.status, r.raw)
	}

	tests := []struct {
		name, app, token, key, body string
		want                        int
	}{
		{"short sha", "web", f.deployer, "k2", `{"ref":"abc123"}`, 422},
		{"uppercase sha", "web", f.deployer, "k3", `{"ref":"` + strings.Repeat("AB", 20) + `"}`, 422},
		{"key with space", "web", f.deployer, "a b", "", 422},
		{"unknown field", "web", f.deployer, "k4", `{"branch":"main"}`, 400},
		{"unknown app", "nope", f.deployer, "k5", "", 404},
		{"read token", "web", f.reader, "k6", "", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if r := f.deploy(tt.app, tt.token, tt.key, tt.body); r.status != tt.want {
				t.Fatalf("status %d, want %d: %s", r.status, tt.want, r.raw)
			}
		})
	}
	for _, id := range []string{"nope", "00000000-0000-4000-8000-000000000000"} {
		if r := f.call("GET", "/v1/operations/"+id, f.reader, "", ""); r.status != 404 {
			t.Fatalf("GET operation %s: %d", id, r.status)
		}
	}
}
