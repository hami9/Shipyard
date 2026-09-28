package routing

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

type fakeRoutes struct {
	rows []store.Route
	err  error
}

func (f *fakeRoutes) ListRoutes(context.Context) ([]store.Route, error) { return f.rows, f.err }

// verifyFake answers on the verify socket the way Caddy would with the
// config the fake admin currently holds: it finds the route for the Host
// header and replies with the status its upstream is set to.
type verifyFake struct {
	caddy    *fakeCaddy
	upstream map[string]int // dial address -> status code
	mu       sync.Mutex
	hosts    []string // Host headers seen
}

func (v *verifyFake) seen() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return strings.Join(v.hosts, ",")
}

func (v *verifyFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	v.caddy.mu.Lock()
	cfg := v.caddy.config
	v.caddy.mu.Unlock()
	v.mu.Lock()
	v.hosts = append(v.hosts, r.Host)
	v.mu.Unlock()
	var c config
	if err := json.Unmarshal([]byte(cfg), &c); err != nil {
		http.Error(w, "no config", http.StatusInternalServerError)
		return
	}
	for _, rt := range c.Apps.HTTP.Servers[VerifyServerName].Routes {
		if len(rt.Match) == 0 || len(rt.Match[0].Host) == 0 || rt.Match[0].Host[0] != r.Host {
			continue
		}
		h := rt.Handle[0]
		if h.Handler == "reverse_proxy" {
			w.WriteHeader(v.upstream[h.Upstreams[0].Dial])
			return
		}
		w.WriteHeader(h.StatusCode)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

type routerFixture struct {
	r      *Router
	caddy  *fakeCaddy
	verify *verifyFake
	routes *fakeRoutes
}

func newRouterFixture(t *testing.T) *routerFixture {
	t.Helper()
	caddy := &fakeCaddy{config: "{}"}
	admin := serveFake(t, caddy)
	dir := filepath.Dir(admin.socket)
	vsock := filepath.Join(dir, "verify.sock")
	ln, err := net.Listen("unix", vsock)
	if err != nil {
		t.Fatal(err)
	}
	vf := &verifyFake{caddy: caddy, upstream: map[string]int{"old:3000": 200, "new:3000": 200, "sick:3000": 502}}
	srv := &http.Server{Handler: vf}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	dep := "d-old"
	routes := &fakeRoutes{rows: []store.Route{
		{AppID: "web", Hostname: "a.example.com", DeploymentID: &dep, Upstream: "old:3000"},
		{AppID: "web", Hostname: "b.example.com", DeploymentID: &dep, Upstream: "old:3000"},
		{AppID: "other", Hostname: "c.example.com", Upstream: "else:80"},
	}}
	r, err := NewRouter(routes, Settings{AdminSocket: admin.socket, VerifySocket: vsock})
	if err != nil {
		t.Fatal(err)
	}
	return &routerFixture{r: r, caddy: caddy, verify: vf, routes: routes}
}

// upstreams maps hostname -> dial address in the loaded config.
func (f *routerFixture) upstreams(t *testing.T) map[string]string {
	t.Helper()
	f.caddy.mu.Lock()
	cfg := f.caddy.config
	f.caddy.mu.Unlock()
	var c config
	if err := json.Unmarshal([]byte(cfg), &c); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, rt := range c.Apps.HTTP.Servers[ServerName].Routes {
		if h := rt.Handle[0]; h.Handler == "reverse_proxy" {
			out[rt.Match[0].Host[0]] = h.Upstreams[0].Dial
		}
	}
	return out
}

func TestRouterSwitch(t *testing.T) {
	f := newRouterFixture(t)
	hosts, err := f.r.Switch(t.Context(), "web", "new:3000", "/healthz")
	if err != nil || strings.Join(hosts, ",") != "a.example.com,b.example.com" {
		t.Fatalf("Switch = %v, %v", hosts, err)
	}
	up := f.upstreams(t)
	// Only this app's routes moved; the other app is untouched.
	if up["a.example.com"] != "new:3000" || up["b.example.com"] != "new:3000" || up["c.example.com"] != "else:80" {
		t.Fatalf("loaded upstreams = %v", up)
	}
	if got := f.verify.seen(); got != "a.example.com,b.example.com" {
		t.Fatalf("verified hosts = %v", got)
	}
	// The routes table itself is the caller's to commit; Restore goes back to it.
	if changed, err := f.r.Restore(t.Context()); err != nil || !changed {
		t.Fatalf("Restore = %v, %v", changed, err)
	}
	if up := f.upstreams(t); up["a.example.com"] != "old:3000" {
		t.Fatalf("after Restore = %v", up)
	}
}

// A candidate Caddy cannot reach fails verification; the caller restores.
func TestRouterSwitchVerifyFails(t *testing.T) {
	f := newRouterFixture(t)
	_, err := f.r.Switch(t.Context(), "web", "sick:3000", "/healthz")
	if err == nil || !strings.Contains(err.Error(), "status 502") || !strings.Contains(err.Error(), "a.example.com") {
		t.Fatalf("err = %v", err)
	}
	if n := strings.Count(f.verify.seen(), "a.example.com"); n != verifyAttempts {
		t.Fatalf("attempts = %d, want %d", n, verifyAttempts)
	}
}

// An app without routes switches nothing and loads nothing.
func TestRouterSwitchNoRoutes(t *testing.T) {
	f := newRouterFixture(t)
	hosts, err := f.r.Switch(t.Context(), "api", "new:3000", "/")
	f.caddy.mu.Lock()
	loads := len(f.caddy.loads)
	f.caddy.mu.Unlock()
	if err != nil || hosts != nil || loads != 0 {
		t.Fatalf("Switch = %v, %v with %d loads", hosts, err, len(f.caddy.loads))
	}
}

func TestRouterErrors(t *testing.T) {
	f := newRouterFixture(t)
	f.routes.err = errors.New("db down")
	if _, err := f.r.Switch(t.Context(), "web", "new:3000", "/"); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("Switch = %v", err)
	}
	if _, err := f.r.Restore(t.Context()); err == nil {
		t.Fatal("Restore without routes succeeded")
	}
	f.routes.err = nil
	// An upstream the renderer refuses never reaches Caddy.
	_, err := f.r.Switch(t.Context(), "web", "unix//var/run/docker.sock", "/")
	f.caddy.mu.Lock()
	loads := len(f.caddy.loads)
	f.caddy.mu.Unlock()
	if !errors.Is(err, ErrInvalid) || loads != 0 {
		t.Fatalf("socket upstream: %v with %d loads", err, loads)
	}
	if _, err := NewRouter(f.routes, Settings{AdminSocket: sock}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("router without verify socket: %v", err)
	}
}
