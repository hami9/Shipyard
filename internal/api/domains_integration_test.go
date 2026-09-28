//go:build integration

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// fakeDNS answers from a table; a missing name is NXDOMAIN.
type fakeDNS struct {
	mu      sync.Mutex
	records map[string][]string
	err     error
	lookups int
}

func (d *fakeDNS) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lookups++
	if d.err != nil {
		return nil, d.err
	}
	recs, ok := d.records[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var out []netip.Addr
	for _, r := range recs {
		out = append(out, netip.MustParseAddr(r))
	}
	return out, nil
}

func (d *fakeDNS) fail(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
}

func (d *fakeDNS) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lookups
}

var serverIP = netip.MustParseAddr("203.0.113.10")

// domainsServer serves the API with a domain policy and fake DNS on f's store.
func (f *apiFixture) domainsServer(policy DomainPolicy, dns *fakeDNS) *httptest.Server {
	srv := httptest.NewServer(NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deps{DB: nil, Tokens: f.s, Audit: f.s, Apps: f.s, Env: f.env, Ops: f.s, Domains: f.s, Resolver: dns, DomainPolicy: policy}))
	f.t.Cleanup(srv.Close)
	return srv
}

func (f *apiFixture) callAt(srv *httptest.Server, method, path, token, body string) response {
	f.t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	return readResponse(res)
}

func TestDomainsAPI(t *testing.T) {
	f := newAPIFixture(t)
	dns := &fakeDNS{records: map[string][]string{
		"web.example.com":   {"203.0.113.10"},
		"dual.example.com":  {"203.0.113.10", "2001:db8::99"},
		"other.example.com": {"198.51.100.7"},
	}}
	srv := f.domainsServer(DomainPolicy{Preflight: true, PublicIPs: []netip.Addr{serverIP}}, dns)
	if r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`); r.status != http.StatusCreated {
		t.Fatal(r.raw)
	}
	add := func(token, host string) response {
		return f.callAt(srv, "POST", "/v1/apps/web/domains", token, `{"hostname":"`+host+`"}`)
	}

	if r := add(f.reader, "web.example.com"); r.status != http.StatusForbidden {
		t.Fatalf("read token: %d", r.status)
	}
	// Normalized, checked, and without a deployment yet.
	r := add(f.admin, "Web.Example.COM.")
	if r.status != http.StatusCreated || r.body["hostname"] != "web.example.com" || r.body["deployment_id"] != nil || r.body["dns_checked_at"] == nil {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	if r := add(f.admin, "web.example.com"); r.status != http.StatusConflict || !strings.Contains(r.raw, "already used by an app") {
		t.Fatalf("duplicate: %d %s", r.status, r.raw)
	}
	for host, want := range map[string]string{
		"*.example.com":     "wildcards are not supported",
		"localhost":         "fully qualified",
		"other.example.com": "resolves to 198.51.100.7, which is not this server (203.0.113.10)",
		"dual.example.com":  "resolves to 2001:db8::99", // one stray record is enough to refuse
		"none.example.com":  "has no A or AAAA record",
	} {
		r := add(f.admin, host)
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.raw, want) || !strings.Contains(r.raw, `"hostname"`) {
			t.Errorf("%s: %d %s", host, r.status, r.raw)
		}
	}
	dns.fail(errors.New("i/o timeout"))
	if r := add(f.admin, "web2.example.com"); r.status != http.StatusServiceUnavailable {
		t.Errorf("DNS failure: %d %s", r.status, r.raw)
	}
	dns.fail(nil)

	list := f.callAt(srv, "GET", "/v1/apps/web/domains", f.reader, "")
	if ds, _ := list.body["domains"].([]any); list.status != 200 || len(ds) != 1 {
		t.Fatalf("list: %d %s", list.status, list.raw)
	}
	if r := f.callAt(srv, "GET", "/v1/apps/nope/domains", f.reader, ""); r.status != http.StatusNotFound {
		t.Errorf("unknown app: %d", r.status)
	}
	if r := f.callAt(srv, "DELETE", "/v1/apps/web/domains/WEB.example.com", f.admin, ""); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.raw)
	}
	if r := f.callAt(srv, "DELETE", "/v1/apps/web/domains/web.example.com", f.admin, ""); r.status != http.StatusNotFound {
		t.Fatalf("second delete: %d", r.status)
	}
}

func TestDomainsPolicy(t *testing.T) {
	f := newAPIFixture(t)
	f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	dns := &fakeDNS{records: map[string][]string{"a.apps.example.com": {"203.0.113.10"}, "evil.net": {"203.0.113.10"}}}
	body := func(h string) string { return `{"hostname":"` + h + `"}` }

	allow := f.domainsServer(DomainPolicy{Preflight: true, PublicIPs: []netip.Addr{serverIP}, Suffixes: []string{"apps.example.com"}}, dns)
	if r := f.callAt(allow, "POST", "/v1/apps/web/domains", f.admin, body("evil.net")); r.status != 422 || !strings.Contains(r.raw, "apps.example.com") {
		t.Errorf("outside the allow-list: %d %s", r.status, r.raw)
	}
	if r := f.callAt(allow, "POST", "/v1/apps/web/domains", f.admin, body("a.apps.example.com")); r.status != http.StatusCreated {
		t.Errorf("inside the allow-list: %d %s", r.status, r.raw)
	}

	// Preflight on without the server's IPs refuses rather than guess.
	unset := f.domainsServer(DomainPolicy{Preflight: true}, dns)
	if r := f.callAt(unset, "POST", "/v1/apps/web/domains", f.admin, body("b.example.org")); r.status != http.StatusServiceUnavailable || !strings.Contains(r.raw, "SHIPYARD_PUBLIC_IPS") {
		t.Errorf("preflight without IPs: %d %s", r.status, r.raw)
	}

	// Preflight off: no lookup, and no check time recorded.
	before := dns.count()
	off := f.domainsServer(DomainPolicy{}, dns)
	r := f.callAt(off, "POST", "/v1/apps/web/domains", f.admin, body("c.example.org"))
	if r.status != http.StatusCreated || r.body["dns_checked_at"] != nil || dns.count() != before {
		t.Errorf("preflight off: %d %s (lookups %d -> %d)", r.status, r.raw, before, dns.count())
	}

	// P2.8: the API's own hostname is not an app domain, in any spelling.
	api := f.domainsServer(DomainPolicy{APIHostname: "shipyard.example.org"}, dns)
	for _, h := range []string{"shipyard.example.org", "Shipyard.Example.org."} {
		if r := f.callAt(api, "POST", "/v1/apps/web/domains", f.admin, body(h)); r.status != http.StatusConflict || !strings.Contains(r.raw, "reserved for the Shipyard API") {
			t.Errorf("API hostname %q: %d %s", h, r.status, r.raw)
		}
	}
}

// A hostname added to an app that is already serving targets its active
// deployment at once, by the deterministic container name.
func TestDomainOnActiveDeployment(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	var a map[string]any
	r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	json.Unmarshal([]byte(r.raw), &a)
	appID := a["id"].(string)

	if _, err := f.s.EnqueueOperation(ctx, store.NewOperation{AppID: appID, Kind: "deploy", IdempotencyKey: "k1"}); err != nil {
		t.Fatal(err)
	}
	op, err := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.s.CreateDeployment(ctx, "w1", store.NewDeployment{OperationID: op.ID, SourceCommitSHA: strings.Repeat("ab", 20)})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []error{
		f.s.RecordImage(ctx, d.ID, "w1", "sha256:"+strings.Repeat("0f", 32), nil),
		f.s.RecordContainer(ctx, d.ID, "w1", strings.Repeat("c1", 32)),
		f.s.MarkHealthChecking(ctx, d.ID, "w1"),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}
	if _, err := f.s.ActivateDeployment(ctx, d.ID, "w1", "", nil); err != nil {
		t.Fatal(err)
	}

	srv := f.domainsServer(DomainPolicy{}, &fakeDNS{})
	r = f.callAt(srv, "POST", "/v1/apps/web/domains", f.admin, `{"hostname":"web.example.com"}`)
	if r.status != http.StatusCreated || r.body["deployment_id"] != d.ID {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	rows, _ := f.s.RoutesByApp(ctx, appID)
	if len(rows) != 1 || rows[0].Upstream != "shipyard-web-"+d.ID+":3000" {
		t.Fatalf("route = %+v", rows)
	}
}
