package github

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI answers the three calls the Reporter makes and records them.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	tokens   int               // installation tokens issued
	perms    []string          // their permissions
	calls    []string          // method path, for deployments and statuses
	bodies   []map[string]any  // their bodies
	auth     map[string]string // path -> Authorization
	failNext int               // status to answer the next deployments call with
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	b, _ := io.ReadAll(r.Body)
	json.Unmarshal(b, &body)
	switch {
	case strings.HasSuffix(r.URL.Path, "/access_tokens"):
		f.tokens++
		p, _ := json.Marshal(body["permissions"])
		f.perms = append(f.perms, string(p))
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"token":"ghs_tok`+string(rune('0'+f.tokens))+`","expires_at":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}`)
	default:
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.bodies = append(f.bodies, body)
		if f.auth == nil {
			f.auth = map[string]string{}
		}
		f.auth[r.URL.Path] = r.Header.Get("Authorization")
		if f.failNext != 0 {
			w.WriteHeader(f.failNext)
			io.WriteString(w, `{"message":"Resource not accessible by integration"}`)
			f.failNext = 0
			return
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":9001}`)
	}
}

func newReporter(t *testing.T) (*fakeAPI, *Reporter) {
	f := &fakeAPI{t: t}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &Reporter{App: &App{ID: "1", Key: key(t), APIURL: srv.URL, HTTP: srv.Client()}}
}

// [GH-DEPLOY]: a deployment of the exact commit (no auto-merge, no required
// contexts) in the app's environment, then its statuses; one token,
// limited to deployments: write, serves them all.
func TestReporter(t *testing.T) {
	f, r := newReporter(t)
	ctx := t.Context()
	id, err := r.CreateDeployment(ctx, 42, "octo/app", Deployment{Ref: strings.Repeat("a", 40), Environment: "web",
		Description: "Shipyard deploy d-1", ShipyardID: "d-1"})
	if err != nil || id != 9001 {
		t.Fatalf("CreateDeployment = %d, %v", id, err)
	}
	long := strings.Repeat("é", 200)
	if err := r.CreateStatus(ctx, 42, "octo/app", id, DeploymentStatus{State: StateSuccess, Description: long,
		EnvironmentURL: "https://web.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateStatus(ctx, 42, "octo/app", 7, DeploymentStatus{State: StateInactive}); err != nil {
		t.Fatal(err)
	}

	if f.tokens != 1 || f.perms[0] != `{"deployments":"write"}` {
		t.Fatalf("tokens %d, permissions %v", f.tokens, f.perms)
	}
	want := []string{"POST /repos/octo/app/deployments", "POST /repos/octo/app/deployments/9001/statuses", "POST /repos/octo/app/deployments/7/statuses"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v", f.calls)
	}
	if f.auth["/repos/octo/app/deployments"] != "Bearer ghs_tok1" {
		t.Fatalf("authorization = %q", f.auth["/repos/octo/app/deployments"])
	}
	d := f.bodies[0]
	if d["ref"] != strings.Repeat("a", 40) || d["auto_merge"] != false || len(d["required_contexts"].([]any)) != 0 ||
		d["environment"] != "web" || d["production_environment"] != true || d["payload"].(map[string]any)["shipyard_deployment"] != "d-1" {
		t.Fatalf("deployment body = %v", d)
	}
	s := f.bodies[1]
	if s["state"] != StateSuccess || s["environment_url"] != "https://web.example.com" || s["auto_inactive"] != false ||
		len([]rune(s["description"].(string))) != maxDescription {
		t.Fatalf("status body = %v", s)
	}
	if _, ok := f.bodies[2]["environment_url"]; ok {
		t.Fatalf("an empty environment_url was sent: %v", f.bodies[2])
	}

	// A token close to expiry is replaced.
	r.App.Now = func() time.Time { return time.Now().Add(56 * time.Minute) }
	r.CreateStatus(ctx, 42, "octo/app", 7, DeploymentStatus{State: StateInactive})
	if f.tokens != 2 {
		t.Fatalf("tokens = %d, want a new one near expiry", f.tokens)
	}
}

func TestReporterFails(t *testing.T) {
	f, r := newReporter(t)
	f.failNext = http.StatusForbidden
	_, err := r.CreateDeployment(t.Context(), 42, "octo/app", Deployment{Ref: "x", Environment: "web"})
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "not accessible") || strings.Contains(err.Error(), "ghs_") {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{"../x", "x/..", "./x", "x/.", "a..b/c", "x", "a/b/c"} {
		if _, err := r.CreateDeployment(t.Context(), 42, bad, Deployment{}); err == nil {
			t.Errorf("repository %q was accepted", bad)
		}
	}
	if err := r.CreateStatus(t.Context(), 42, "octo/a/b", 1, DeploymentStatus{State: StateFailure}); err == nil {
		t.Fatal("a bad repository was accepted")
	}
}
