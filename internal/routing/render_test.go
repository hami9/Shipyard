package routing

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const sock = "/run/shipyard/caddy/caddy-admin.sock"

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file; got:\n%s", path, got)
	}
}

func TestRenderGolden(t *testing.T) {
	apps := []Route{
		// Out of order on purpose: output is sorted.
		{Hostname: "web.example.com", Upstream: "shipyard-web-11111111-1111-4111-8111-111111111111:3000"},
		{Hostname: "idle.example.com"},
		{Hostname: "api-docs.example.org", Upstream: "172.20.0.5:8080"},
	}
	for name, tc := range map[string]struct {
		s      Settings
		routes []Route
	}{
		// Production shape: API through Caddy, default CAs.
		"full": {Settings{AdminSocket: sock, APIHostname: "shipyard.example.com", APIUpstream: "unix//run/shipyard/api/api.sock"}, apps},
		// A fresh install: only the admin socket, nothing served.
		"empty":    {Settings{AdminSocket: sock}, nil},
		"staging":  {Settings{AdminSocket: sock, CA: CAStaging, ACMEEmail: "ops@example.com"}, apps[:1]},
		"internal": {Settings{AdminSocket: sock, CA: CAInternal}, apps[:1]},
		"email":    {Settings{AdminSocket: sock, ACMEEmail: "ops@example.com"}, apps[:1]},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Render(tc.s, tc.routes)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(got) {
				t.Fatal("invalid JSON")
			}
			golden(t, name, got)
		})
	}
}

// Rendering is a pure function: input order does not matter and the input
// is not modified.
func TestRenderDeterministic(t *testing.T) {
	a := []Route{{Hostname: "b.example.com", Upstream: "x:1"}, {Hostname: "a.example.com"}}
	b := []Route{a[1], a[0]}
	s := Settings{AdminSocket: sock}
	ra, _ := Render(s, a)
	rb, _ := Render(s, b)
	if !bytes.Equal(ra, rb) {
		t.Fatal("output depends on input order")
	}
	if a[0].Hostname != "b.example.com" {
		t.Fatal("Render sorted the caller's slice")
	}
}

func TestFromStore(t *testing.T) {
	dep := "d1"
	got := FromStore([]store.Route{{Hostname: "a.example.com", DeploymentID: &dep, Upstream: "web:80"}, {Hostname: "b.example.com"}})
	if len(got) != 2 || got[0] != (Route{"a.example.com", "web:80"}) || got[1] != (Route{Hostname: "b.example.com"}) {
		t.Fatalf("FromStore = %+v", got)
	}
}

// Invariant 12: every rendered config keeps the admin API on the socket.
func TestRenderKeepsAdminSocket(t *testing.T) {
	got, _ := Render(Settings{AdminSocket: sock}, []Route{{Hostname: "web.example.com", Upstream: "web:80"}})
	var cfg struct {
		Admin struct{ Listen string } `json:"admin"`
	}
	if err := json.Unmarshal(got, &cfg); err != nil || cfg.Admin.Listen != "unix/"+sock+"|0660" {
		t.Fatalf("admin.listen = %q, %v", cfg.Admin.Listen, err)
	}
}

func TestRenderRejects(t *testing.T) {
	ok := Settings{AdminSocket: sock}
	for name, tc := range map[string]struct {
		s      Settings
		routes []Route
	}{
		"relative admin socket":     {Settings{AdminSocket: "caddy.sock"}, nil},
		"admin socket with perms":   {Settings{AdminSocket: sock + "|0666"}, nil},
		"admin socket unclean":      {Settings{AdminSocket: "/run/../tmp/x.sock"}, nil},
		"uppercase hostname":        {ok, []Route{{Hostname: "Web.example.com"}}},
		"single-label hostname":     {ok, []Route{{Hostname: "localhost"}}},
		"wildcard hostname":         {ok, []Route{{Hostname: "*.example.com"}}},
		"placeholder hostname":      {ok, []Route{{Hostname: "{http.request.host}"}}},
		"duplicate hostname":        {ok, []Route{{Hostname: "a.example.com"}, {Hostname: "a.example.com"}}},
		"app on the API hostname":   {Settings{AdminSocket: sock, APIHostname: "a.example.com", APIUpstream: "api:80"}, []Route{{Hostname: "a.example.com"}}},
		"upstream without port":     {ok, []Route{{Hostname: "a.example.com", Upstream: "web"}}},
		"upstream port range":       {ok, []Route{{Hostname: "a.example.com", Upstream: "web:80-90"}}},
		"upstream port zero":        {ok, []Route{{Hostname: "a.example.com", Upstream: "web:0"}}},
		"upstream network prefix":   {ok, []Route{{Hostname: "a.example.com", Upstream: "tcp/web:80"}}},
		"upstream placeholder":      {ok, []Route{{Hostname: "a.example.com", Upstream: "{env.X}:80"}}},
		"app upstream on a socket":  {ok, []Route{{Hostname: "a.example.com", Upstream: "unix//var/run/docker.sock"}}},
		"API host without upstream": {Settings{AdminSocket: sock, APIHostname: "api.example.com"}, nil},
		"API relative socket":       {Settings{AdminSocket: sock, APIHostname: "api.example.com", APIUpstream: "unix/api.sock"}, nil},
		"unknown CA":                {Settings{AdminSocket: sock, CA: "zerossl"}, nil},
		"bad email":                 {Settings{AdminSocket: sock, ACMEEmail: "not-an-email"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := Render(tc.s, tc.routes); !errors.Is(err, ErrInvalid) || out != nil {
				t.Fatalf("err = %v, output %d bytes", err, len(out))
			}
		})
	}
}
