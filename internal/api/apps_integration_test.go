//go:build integration

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/logging"
	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
	"github.com/hami9/shipyard/migrations"
)

type apiFixture struct {
	t        *testing.T
	srv      *httptest.Server
	s        *store.Store
	env      *secrets.Env
	admin    string
	reader   string
	deployer string
	logs     *bytes.Buffer
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	ctx := t.Context()
	pool, err := store.Open(ctx, storetest.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := store.Migrate(ctx, pool, ms); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	u, err := s.CreateUser(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	token := func(scope string) string {
		plain, prefix, hash := NewToken()
		if _, err := s.CreateToken(ctx, store.NewToken{UserID: u.ID, Name: scope, Prefix: prefix, Hash: hash, Scopes: []string{scope}}); err != nil {
			t.Fatal(err)
		}
		return plain
	}
	f := &apiFixture{t: t, s: s, admin: token(ScopeAdmin), reader: token(ScopeRead), deployer: token(ScopeDeploy), logs: &bytes.Buffer{}}
	log := logging.New(f.logs, slog.LevelDebug, logging.FormatJSON)
	keys, err := secrets.NewKeyring("test", map[string][]byte{"test": secrets.GenerateKey()})
	if err != nil {
		t.Fatal(err)
	}
	f.env = secrets.NewEnv(keys, s)
	f.srv = httptest.NewServer(conform(t, NewHandler(log, Deps{DB: pool, Tokens: s, Audit: s, Apps: s, Env: f.env, Ops: s,
		StreamPoll: 20 * time.Millisecond, StreamKeepalive: 200 * time.Millisecond})))
	t.Cleanup(f.srv.Close)
	return f
}

type response struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (f *apiFixture) call(method, path, token, contentType, body string) response {
	f.t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	return readResponse(res)
}

func readResponse(res *http.Response) response {
	raw, _ := io.ReadAll(res.Body)
	out := response{status: res.StatusCode, header: res.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (f *apiFixture) json(method, path, body string) response {
	f.t.Helper()
	return f.call(method, path, f.admin, "application/json", body)
}

func fieldsOf(r response) []string {
	var out []string
	errs, _ := r.body["errors"].([]any)
	for _, e := range errs {
		out = append(out, e.(map[string]any)["field"].(string))
	}
	return out
}

func TestAppsCRUD(t *testing.T) {
	f := newAPIFixture(t)

	r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	id := r.body["id"].(string)
	if r.header.Get("Location") != "/v1/apps/"+id || r.body["health_timeout"] != "1m0s" || r.body["memory_limit"] != float64(512<<20) ||
		r.body["dockerfile_path"] != "Dockerfile" || r.body["stop_timeout"] != "10s" {
		t.Fatalf("create response: %s (Location %q)", r.raw, r.header.Get("Location"))
	}

	full := `{"slug":"api","repo":"hami9/api","branch":"release/1.x","port":8080,"dockerfile_path":"docker/Dockerfile",
		"build_context":"services/api","health_path":"/healthz","health_timeout":"90s","cpu_limit":0.5,
		"memory_limit":268435456,"stop_timeout":"30s","auto_deploy":true,"github_installation_id":42}`
	if r := f.json("POST", "/v1/apps", full); r.status != http.StatusCreated || r.body["health_timeout"] != "1m30s" || r.body["cpu_limit"] != 0.5 {
		t.Fatalf("create full: %d %s", r.status, r.raw)
	}

	for _, ref := range []string{"web", id} {
		if r := f.call("GET", "/v1/apps/"+ref, f.reader, "", ""); r.status != http.StatusOK || r.body["id"] != id {
			t.Fatalf("GET %s: %d %s", ref, r.status, r.raw)
		}
	}
	for _, ref := range []string{"nope", "00000000-0000-4000-8000-000000000000", "Not_A_Slug"} {
		if r := f.call("GET", "/v1/apps/"+ref, f.reader, "", ""); r.status != http.StatusNotFound || r.header.Get("Content-Type") != "application/problem+json" {
			t.Fatalf("GET %s: %d %s", ref, r.status, r.raw)
		}
	}
	if r := f.call("GET", "/v1/apps", f.reader, "", ""); r.status != http.StatusOK || len(r.body["apps"].([]any)) != 2 {
		t.Fatalf("list: %d %s", r.status, r.raw)
	}

	r = f.json("PATCH", "/v1/apps/web", `{"branch":"dev","health_timeout":"5s","auto_deploy":true}`)
	if r.status != http.StatusOK || r.body["branch"] != "dev" || r.body["health_timeout"] != "5s" || r.body["port"] != float64(3000) {
		t.Fatalf("patch: %d %s", r.status, r.raw)
	}
	if r := f.json("PATCH", "/v1/apps/web", `{"slug":"x","repo":"a/b","build_context":"../.."}`); r.status != http.StatusUnprocessableEntity ||
		strings.Join(fieldsOf(r), ",") != "slug,repo,build_context" {
		t.Fatalf("patch immutable fields: %d %s", r.status, r.raw)
	}

	// Delete queues an operation (P3.8, TestAppDeleteEndpoint): the app is
	// still there until the worker has removed it.
	if r := f.call("DELETE", "/v1/apps/web", f.admin, "", ""); r.status != http.StatusAccepted || opField(r, "kind") != "delete" {
		t.Fatalf("delete: %d %s", r.status, r.raw)
	}
	if r := f.call("GET", "/v1/apps/web", f.reader, "", ""); r.status != http.StatusOK {
		t.Fatalf("GET while the delete is queued: %d", r.status)
	}
	if r := f.call("DELETE", "/v1/apps/nope", f.admin, "", ""); r.status != http.StatusNotFound {
		t.Fatalf("delete of an unknown app: %d", r.status)
	}
}

func TestAppsRejects(t *testing.T) {
	f := newAPIFixture(t)
	if r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`); r.status != http.StatusCreated {
		t.Fatalf("setup: %d %s", r.status, r.raw)
	}

	r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	if r.status != http.StatusConflict || r.body["detail"] != "an app with this slug already exists" {
		t.Fatalf("duplicate slug: %d %s", r.status, r.raw)
	}

	r = f.json("POST", "/v1/apps", `{"slug":"Bad_Slug","repo":"x","branch":"--upload-pack=x","port":0,"dockerfile_path":"../Dockerfile","memory_limit":1}`)
	if r.status != http.StatusUnprocessableEntity || strings.Join(fieldsOf(r), ",") != "slug,repo,branch,dockerfile_path,port,memory_limit" {
		t.Fatalf("invalid fields: %d %s", r.status, r.raw)
	}

	tests := []struct {
		name, ctype, body string
		want              int
	}{
		{"unknown field", "application/json", `{"slug":"a1","repo":"a/b","branch":"m","port":1,"privileged":true}`, 400},
		{"not JSON", "application/json", `{"slug":`, 400},
		{"two objects", "application/json", `{"slug":"a2"} {}`, 400},
		{"bad duration", "application/json", `{"slug":"a3","repo":"a/b","branch":"m","port":1,"health_timeout":"soon"}`, 400},
		{"wrong type", "application/json", `{"slug":"a4","repo":"a/b","branch":"m","port":"80"}`, 400},
		{"form body", "application/x-www-form-urlencoded", `slug=a5`, 415},
		{"no content type", "", `{}`, 415},
		{"too large", "application/json", `{"slug":"` + strings.Repeat("a", maxBodyBytes) + `"}`, 413},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := f.call("POST", "/v1/apps", f.admin, tt.ctype, tt.body)
			if r.status != tt.want || r.header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status %d (want %d): %s", r.status, tt.want, r.raw)
			}
		})
	}

	if r := f.call("POST", "/v1/apps", f.reader, "application/json", `{"slug":"ro","repo":"a/b","branch":"m","port":1}`); r.status != http.StatusForbidden {
		t.Fatalf("read token created an app: %d", r.status)
	}
	if strings.Contains(f.logs.String(), `"level":"ERROR"`) {
		t.Fatalf("client errors were logged as server errors:\n%s", f.logs.String())
	}
}

// P3.8: DELETE queues the app's delete operation behind a running one,
// cancelling queued ones. Until the worker has finished, the app exists
// and refuses new deploys; a repeated delete is the same operation. When the
// worker's last step removes the app, the operation goes with it: its event
// stream ends as succeeded, both answer 404, and the audit events stay.
func TestAppDeleteEndpoint(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	id := r.body["id"].(string)
	f.deploy("web", f.deployer, "runs", "")
	running, err := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	waiting := opField(f.deploy("web", f.deployer, "waits", ""), "id").(string)

	for name, token := range map[string]string{"read": f.reader, "deploy": f.deployer} {
		if r := f.call("DELETE", "/v1/apps/web", token, "", ""); r.status != http.StatusForbidden {
			t.Fatalf("delete with a %s token: %d", name, r.status)
		}
	}
	del := f.call("DELETE", "/v1/apps/web", f.admin, "", "")
	delID, _ := opField(del, "id").(string)
	if del.status != http.StatusAccepted || opField(del, "kind") != "delete" || opField(del, "status") != store.OpQueued ||
		del.header.Get("Location") != "/v1/operations/"+delID || !strings.Contains(del.raw, waiting) {
		t.Fatalf("delete: %d %s", del.status, del.raw)
	}
	if got, _ := f.s.OperationByID(ctx, running.ID); got.Status != store.OpRunning {
		t.Fatalf("the running deploy is now %s", got.Status)
	}
	if got, _ := f.s.OperationByID(ctx, waiting); got.Status != store.OpCancelled {
		t.Fatalf("the queued deploy is %s, want cancelled", got.Status)
	}
	if again := f.call("DELETE", "/v1/apps/web", f.admin, "", ""); again.status != http.StatusOK || opField(again, "id") != delID {
		t.Fatalf("second delete: %d %s", again.status, again.raw)
	}
	if r := f.deploy("web", f.deployer, "late", ""); r.status != http.StatusConflict || !strings.Contains(r.raw, "being deleted") {
		t.Fatalf("deploy while deleting: %d %s", r.status, r.raw)
	}
	if r := f.call("GET", "/v1/apps/web", f.reader, "", ""); r.status != http.StatusOK {
		t.Fatalf("GET while deleting: %d", r.status)
	}

	// The worker's part: the deploy ends, the delete is claimed and runs.
	s, _ := f.stream(delID, f.reader, "")
	s.next() // retry
	if err := f.s.CompleteOperation(ctx, running.ID, "w1"); err != nil {
		t.Fatal(err)
	}
	if op, err := f.s.ClaimOperation(ctx, "w1", time.Minute); err != nil || op.ID != delID {
		t.Fatalf("claim: %+v, %v", op, err)
	}
	if _, err := f.s.AppendOperationEvent(ctx, delID, store.LevelInfo, "1 containers removed"); err != nil {
		t.Fatal(err)
	}
	s.wantEvent("1", "1 containers removed")
	if _, err := f.s.StopServing(ctx, id, delID, "w1"); err != nil {
		t.Fatal(err)
	}
	audit := store.AuditEvent{Actor: "system", Action: "app.delete", Target: "app:web", Result: store.AuditSuccess}
	if err := f.s.FinishAppDelete(ctx, id, delID, "w1", audit); err != nil {
		t.Fatal(err)
	}
	s.wantEnd(store.OpSucceeded)
	for _, path := range []string{"/v1/apps/web", "/v1/operations/" + delID, "/v1/operations/" + running.ID} {
		if r := f.call("GET", path, f.reader, "", ""); r.status != http.StatusNotFound {
			t.Fatalf("GET %s after the delete: %d", path, r.status)
		}
	}
	if r := f.call("DELETE", "/v1/apps/web", f.admin, "", ""); r.status != http.StatusNotFound {
		t.Fatalf("delete of a deleted app: %d", r.status)
	}

	events, _ := f.s.AuditEvents(ctx, 20)
	var got []string
	for _, e := range events {
		got = append(got, e.Actor[:strings.IndexAny(e.Actor+":", ":")]+" "+e.Action+" "+e.Result)
	}
	for _, want := range []string{"system app.delete success", "token DELETE /v1/apps/{app} success", "token DELETE /v1/apps/{app} denied",
		"token POST /v1/apps/{app}/deployments failure"} {
		if !slices.Contains(got, want) {
			t.Fatalf("audit lacks %q: %v", want, got)
		}
	}
}
