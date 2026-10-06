// Package routing renders Caddy's complete JSON config from the routes table
// (ADR-0003). The config is a pure function of its input, so the reconciler
// can always re-render and reload it (invariant 2).
package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hami9/shipyard/internal/store"
)

// Certificate authorities for Settings.CA.
const (
	// CADefault leaves issuance to Caddy's defaults (Let's Encrypt, then
	// ZeroSSL) [CADDY-HTTPS]; with an ACME email, Let's Encrypt only.
	CADefault = ""
	// CAStaging is the Let's Encrypt staging CA, for development and CI
	// [LE-LIMITS].
	CAStaging = "staging"
	// CAInternal is Caddy's local CA, for tests on hosts without public DNS.
	CAInternal = "internal"
)

// StagingDirectory is the Let's Encrypt staging ACME directory [LE-STAGING].
const StagingDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"

// ServerName names Caddy's one HTTP server; tests and the admin client
// address config paths under it.
const ServerName = "shipyard"

// VerifyServerName names the plain-HTTP verification server (Settings.VerifySocket),
// and VerifySocketName its socket's file name next to the admin socket.
const (
	VerifyServerName = "verify"
	VerifySocketName = "caddy-verify.sock"
)

// NoDeploymentBody answers a hostname whose app has nothing active yet.
const NoDeploymentBody = "no active deployment\n"

// ErrInvalid means the input cannot produce a safe config; nothing is
// rendered, so nothing broken is ever loaded.
var ErrInvalid = errors.New("invalid routing input")

// Route is one hostname. Upstream is the Caddy dial address of the app's
// active deployment (host:port on the app network); empty means none yet.
type Route struct {
	Hostname string
	Upstream string
}

// Settings is everything in the config that does not come from routes.
type Settings struct {
	// AdminSocket keeps the admin API on the edge's socket: a loaded config
	// overrides CADDY_ADMIN, so leaving it out would move the admin API to
	// TCP (invariant 12) [CADDY-API].
	AdminSocket string
	// APIHostname serves /v1/* and /hooks/github from APIUpstream; empty
	// renders no API route.
	APIHostname string
	APIUpstream string // host:port or unix//absolute/path
	// WebDir, with an API hostname, serves the web UI from this directory
	// (as Caddy sees it, mounted read-only) on every other path of that
	// hostname (ADR-0016); empty keeps those paths 404.
	WebDir    string
	CA        string // CADefault, CAStaging, or CAInternal
	ACMEEmail string
	// VerifySocket, when set, adds a plain-HTTP server on this Unix socket
	// with exactly the same routes and no automatic HTTPS. The worker checks
	// a route through Caddy there, by Host header, before it commits a
	// switch (ARCHITECTURE §5 step 7): it proves the routing without waiting
	// for a certificate.
	VerifySocket string
}

// Caddy's JSON config, only the parts Shipyard uses. Field names follow
// `caddy adapt` output of Caddy 2.11.4 [CADDY-JSON].
type (
	config struct {
		Admin admin `json:"admin"`
		Apps  apps  `json:"apps"`
	}
	admin struct {
		Listen string `json:"listen"`
	}
	apps struct {
		HTTP httpApp `json:"http"`
		TLS  *tlsApp `json:"tls,omitempty"`
	}
	httpApp struct {
		Servers map[string]server `json:"servers"`
	}
	server struct {
		Listen         []string   `json:"listen"`
		Routes         []route    `json:"routes"`
		AutomaticHTTPS *autoHTTPS `json:"automatic_https,omitempty"`
	}
	autoHTTPS struct {
		Skip []string `json:"skip,omitempty"`
	}
	route struct {
		Match    []match   `json:"match,omitempty"`
		Handle   []handler `json:"handle"`
		Terminal bool      `json:"terminal,omitempty"`
	}
	match struct {
		Host []string   `json:"host,omitempty"`
		Path []string   `json:"path,omitempty"`
		File *fileMatch `json:"file,omitempty"`
	}
	fileMatch struct {
		Root     string   `json:"root"`
		TryFiles []string `json:"try_files"`
	}
	handler struct {
		Handler    string     `json:"handler"`
		Upstreams  []upstream `json:"upstreams,omitempty"`
		Routes     []route    `json:"routes,omitempty"`
		StatusCode int        `json:"status_code,omitempty"`
		Body       string     `json:"body,omitempty"`
		Root       string     `json:"root,omitempty"`     // file_server
		URI        string     `json:"uri,omitempty"`      // rewrite
		Response   *headerOps `json:"response,omitempty"` // headers
	}
	headerOps struct {
		Set map[string][]string `json:"set"`
	}
	upstream struct {
		Dial string `json:"dial"`
	}
	tlsApp struct {
		Automation automation `json:"automation"`
	}
	automation struct {
		Policies []policy `json:"policies"`
	}
	policy struct {
		Issuers []issuer `json:"issuers"`
	}
	issuer struct {
		Module string `json:"module"`
		CA     string `json:"ca,omitempty"`
		Email  string `json:"email,omitempty"`
	}
)

var (
	// hostnameRE mirrors the routes.hostname CHECK in the schema.
	hostnameRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// dialHostRE is a container or host name on the app network.
	dialHostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9_.-]{0,126}[a-z0-9])?$`)
	emailRE    = regexp.MustCompile(`^[^@\s"\\]{1,64}@[a-z0-9.-]{1,253}$`)
)

// Render returns the complete config for these routes, deterministically:
// the same input always gives the same bytes. It refuses invalid input
// rather than render a partial config.
func Render(s Settings, routes []Route) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	routes = slices.Clone(routes)
	slices.SortFunc(routes, func(a, b Route) int { return strings.Compare(a.Hostname, b.Hostname) })
	seen := map[string]bool{s.APIHostname: s.APIHostname != ""}
	var out []route
	var hosts []string
	if s.APIHostname != "" {
		out = append(out, apiRoute(s))
		hosts = append(hosts, s.APIHostname)
	}
	for _, r := range routes {
		if !hostnameRE.MatchString(r.Hostname) || len(r.Hostname) > 253 {
			return nil, fmt.Errorf("%w: hostname %q", ErrInvalid, r.Hostname)
		}
		if seen[r.Hostname] {
			return nil, fmt.Errorf("%w: hostname %q is used twice", ErrInvalid, r.Hostname)
		}
		seen[r.Hostname] = true
		h := handler{Handler: "static_response", StatusCode: 503, Body: NoDeploymentBody}
		if r.Upstream != "" {
			if err := checkDial(r.Upstream, false); err != nil {
				return nil, fmt.Errorf("%w: upstream of %s: %v", ErrInvalid, r.Hostname, err)
			}
			h = handler{Handler: "reverse_proxy", Upstreams: []upstream{{Dial: r.Upstream}}}
		}
		out = append(out, route{Match: []match{{Host: []string{r.Hostname}}}, Handle: []handler{h}, Terminal: true})
		hosts = append(hosts, r.Hostname)
	}
	servers := map[string]server{
		// Only :443 is listed: automatic HTTPS adds the :80 listener for
		// redirects and HTTP challenges [CADDY-HTTPS].
		ServerName: {Listen: []string{":443"}, Routes: orEmpty(out)},
	}
	if s.VerifySocket != "" {
		// The same routes over plain HTTP on a socket; skipping every host
		// there is how `caddy adapt` expresses an http:// site [CADDY-JSON].
		v := server{Listen: []string{"unix/" + s.VerifySocket + "|0660"}, Routes: orEmpty(out)}
		if len(hosts) > 0 {
			v.AutomaticHTTPS = &autoHTTPS{Skip: slices.Sorted(slices.Values(hosts))}
		}
		servers[VerifyServerName] = v
	}
	cfg := config{
		Admin: admin{Listen: "unix/" + s.AdminSocket + "|0660"},
		Apps:  apps{HTTP: httpApp{Servers: servers}},
	}
	if is := s.issuers(); is != nil {
		cfg.Apps.TLS = &tlsApp{Automation: automation{Policies: []policy{{Issuers: is}}}}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WebCSP is the web UI's Content-Security-Policy. The page has no inline
// script or style (ADR-0016), so only same-origin files run, the only
// requests are to the API on the same origin, and nothing may frame it.
const WebCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// apiRoute sends the API's paths to the API. Everything else on its
// hostname is the web UI when there is one, and 404 when not, so nothing
// else of the host is exposed.
func apiRoute(s Settings) route {
	proxy := handler{Handler: "reverse_proxy", Upstreams: []upstream{{Dial: s.APIUpstream}}}
	rest := []route{{Handle: []handler{{Handler: "static_response", StatusCode: 404}}}}
	if s.WebDir != "" {
		rest = webRoutes(s.WebDir)
	}
	return route{
		Match: []match{{Host: []string{s.APIHostname}}},
		Handle: []handler{{Handler: "subroute", Routes: append([]route{
			{Match: []match{{Path: []string{"/v1/*", "/hooks/github"}}}, Handle: []handler{proxy}, Terminal: true},
		}, rest...)}},
		Terminal: true,
	}
}

// webRoutes serve the UI as `caddy adapt` renders `header`, `try_files
// {path} /index.html` and `file_server` [CADDY-JSON]: a path that is no
// file gets index.html, so the app's own links work on reload. Hashed
// assets are cached for good; index.html is revalidated, so a new release
// is picked up at once.
func webRoutes(dir string) []route {
	set := func(h map[string][]string) handler { return handler{Handler: "headers", Response: &headerOps{Set: h}} }
	return []route{
		{Handle: []handler{set(map[string][]string{
			"Content-Security-Policy":    {WebCSP},
			"X-Content-Type-Options":     {"nosniff"},
			"X-Frame-Options":            {"DENY"},
			"Referrer-Policy":            {"no-referrer"},
			"Cross-Origin-Opener-Policy": {"same-origin"},
			"Cache-Control":              {"no-cache"},
		})}},
		{Match: []match{{Path: []string{"/assets/*"}}}, Handle: []handler{set(map[string][]string{
			"Cache-Control": {"public, max-age=31536000, immutable"},
		})}},
		{
			Match:  []match{{File: &fileMatch{Root: dir, TryFiles: []string{"{http.request.uri.path}", "/index.html"}}}},
			Handle: []handler{{Handler: "rewrite", URI: "{http.matchers.file.relative}"}},
		},
		{Handle: []handler{{Handler: "file_server", Root: dir}}},
	}
}

func (s Settings) issuers() []issuer {
	switch s.CA {
	case CAInternal:
		return []issuer{{Module: "internal"}}
	case CAStaging:
		return []issuer{{Module: "acme", CA: StagingDirectory, Email: s.ACMEEmail}}
	}
	if s.ACMEEmail != "" {
		return []issuer{{Module: "acme", Email: s.ACMEEmail}}
	}
	return nil
}

func (s Settings) validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	switch {
	case !cleanSocket(s.AdminSocket):
		return bad("admin socket %q must be a clean absolute path", s.AdminSocket)
	case s.VerifySocket != "" && (!cleanSocket(s.VerifySocket) || s.VerifySocket == s.AdminSocket):
		return bad("verify socket %q must be a clean absolute path other than the admin socket", s.VerifySocket)
	case s.APIHostname != "" && !hostnameRE.MatchString(s.APIHostname):
		return bad("API hostname %q", s.APIHostname)
	case s.APIHostname != "" && s.APIUpstream == "":
		return bad("API hostname without an API upstream")
	case s.WebDir != "" && s.APIHostname == "":
		return bad("a web UI directory without an API hostname to serve it on")
	case s.WebDir != "" && (!cleanSocket(s.WebDir) || strings.ContainsAny(s.WebDir, "{}")):
		// Caddy expands {placeholders} in a root.
		return bad("web UI directory %q must be a clean absolute path", s.WebDir)
	case s.CA != CADefault && s.CA != CAStaging && s.CA != CAInternal:
		return bad("CA %q", s.CA)
	case s.ACMEEmail != "" && !emailRE.MatchString(s.ACMEEmail):
		return bad("ACME email %q", s.ACMEEmail)
	}
	if s.APIHostname != "" {
		if err := checkDial(s.APIUpstream, true); err != nil {
			return bad("API upstream: %v", err)
		}
	}
	return nil
}

// checkDial accepts host:port, and for the API also unix//absolute/path, so
// nothing else (a network prefix, a port range, a placeholder) reaches Caddy.
func checkDial(d string, allowUnix bool) error {
	if path, ok := strings.CutPrefix(d, "unix/"); ok {
		if !allowUnix || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "|{} \t\n") {
			return fmt.Errorf("%q is not an allowed unix socket address", d)
		}
		return nil
	}
	host, port, err := net.SplitHostPort(d)
	if err != nil {
		return fmt.Errorf("%q: %v", d, err)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 || strconv.Itoa(p) != port {
		return fmt.Errorf("%q: invalid port", d)
	}
	if _, err := netip.ParseAddr(host); err != nil && !dialHostRE.MatchString(host) {
		return fmt.Errorf("%q: invalid host", d)
	}
	return nil
}

// cleanSocket accepts a clean absolute path without characters that Caddy's
// network addresses give meaning to (the |mode suffix, ports, lists).
func cleanSocket(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, "|,: \t\n")
}

// FromStore turns routes table rows into renderer input.
func FromStore(rows []store.Route) []Route {
	out := make([]Route, len(rows))
	for i, r := range rows {
		out[i] = Route{Hostname: r.Hostname, Upstream: r.Upstream}
	}
	return out
}

func orEmpty(r []route) []route {
	if r == nil {
		return []route{}
	}
	return r
}
