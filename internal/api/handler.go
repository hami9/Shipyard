// Package api implements shipyard-api's HTTP surface. The API validates
// requests and records intent in PostgreSQL; it never touches Docker, Caddy,
// or repository code (CLAUDE.md invariant 1).
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/hami9/shipyard/internal/metrics"
)

const readinessTimeout = 2 * time.Second

// Pinger reports whether the database is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the handler's dependencies, declared here as narrow interfaces
// and satisfied by *store.Store in production.
type Deps struct {
	DB     Pinger
	Tokens TokenStore
	// TokenAdmin serves /v1/tokens (ADR-0011).
	TokenAdmin TokenAdmin
	Audit      AuditRecorder
	Apps       AppStore
	Env        EnvStore
	Ops        OperationStore
	// Domains, Resolver, and DomainPolicy serve /v1/apps/{app}/domains.
	Domains      DomainStore
	Resolver     Resolver
	DomainPolicy DomainPolicy
	// StreamPoll and StreamKeepalive tune event and log streams; zero means
	// the defaults (tests shorten them).
	StreamPoll, StreamKeepalive time.Duration
	// Logs reads app logs from the worker (ADR-0008); nil answers 503.
	Logs LogSource
	// WebhookSecret verifies POST /hooks/github; empty answers 404.
	// Pushes receives the verified pushes; nil ignores them.
	WebhookSecret []byte
	Pushes        PushSink
	// Limits caps requests and failed authentications per client; the
	// zero value turns both off.
	Limits RateLimits
	// Metrics, when set, gets the API's counters (ADR-0013).
	Metrics *metrics.Registry
}

// NewHandler returns the root handler with middleware applied. Health
// endpoints are public, /hooks/github verifies its own signature, and every
// /v1 route goes through auth.protect. Everything but the health endpoints
// is rate limited per client (d.Limits).
func NewHandler(log *slog.Logger, d Deps) http.Handler {
	mux, _ := newMux(log, d)
	m := newAPIMetrics(d.Metrics)
	lim := newLimiter(log, d.Limits)
	lim.metrics = m
	return withRequestID(withAccessLog(log, m.count(lim.limit(mux))))
}

// routeInfo is a registered route and the scope it requires ("" for the
// public ones); api/openapi.json must list the same (TestOpenAPIRoutes).
type routeInfo struct{ pattern, scope string }

func newMux(log *slog.Logger, d Deps) (*http.ServeMux, []routeInfo) {
	auth := &authenticator{log: log, tokens: d.Tokens, audit: d.Audit}
	var routes []routeInfo
	route := func(mux *http.ServeMux, pattern, scope string, h http.HandlerFunc) {
		mux.Handle(pattern, auth.protect(scope, h))
		routes = append(routes, routeInfo{pattern, scope})
	}
	apps := &appHandlers{log: log, apps: d.Apps}
	env := &envHandlers{appHandlers: apps, env: d.Env}
	ops := &opHandlers{appHandlers: apps, ops: d.Ops, poll: d.StreamPoll, keepalive: d.StreamKeepalive}
	if ops.poll <= 0 {
		ops.poll = defaultStreamPoll
	}
	if ops.keepalive <= 0 {
		ops.keepalive = defaultStreamKeepalive
	}
	logs := &logHandlers{appHandlers: apps, source: d.Logs, keepalive: ops.keepalive}
	domains := &domainHandlers{appHandlers: apps, routes: d.Domains, resolver: d.Resolver, policy: d.DomainPolicy}
	hooks := &webhookHandlers{log: log, secret: d.WebhookSecret, sink: d.Pushes}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz(log, d.DB))
	// Authenticated by its HMAC signature, not a token.
	mux.HandleFunc("POST /hooks/github", hooks.github)
	routes = append(routes, routeInfo{"GET /healthz", ""}, routeInfo{"GET /readyz", ""}, routeInfo{"POST /hooks/github", ""})
	route(mux, "GET /v1/whoami", ScopeRead, handleWhoami)
	tokens := &tokenHandlers{log: log, tokens: d.TokenAdmin, now: time.Now}
	route(mux, "GET /v1/tokens", ScopeAdmin, tokens.list)
	route(mux, "DELETE /v1/tokens/{prefix}", ScopeAdmin, tokens.revoke)
	route(mux, "POST /v1/tokens/self/rotate", ScopeRead, tokens.rotateSelf)
	route(mux, "GET /v1/apps", ScopeRead, apps.list)
	route(mux, "POST /v1/apps", ScopeAdmin, apps.create)
	route(mux, "GET /v1/apps/{app}", ScopeRead, apps.get)
	route(mux, "PATCH /v1/apps/{app}", ScopeAdmin, apps.update)
	route(mux, "DELETE /v1/apps/{app}", ScopeAdmin, ops.deleteApp)
	route(mux, "GET /v1/apps/{app}/env", ScopeRead, env.list)
	route(mux, "PUT /v1/apps/{app}/env/{key}", ScopeAdmin, env.set)
	route(mux, "DELETE /v1/apps/{app}/env/{key}", ScopeAdmin, env.unset)
	route(mux, "GET /v1/apps/{app}/domains", ScopeRead, domains.list)
	route(mux, "POST /v1/apps/{app}/domains", ScopeAdmin, domains.add)
	route(mux, "DELETE /v1/apps/{app}/domains/{hostname}", ScopeAdmin, domains.remove)
	route(mux, "GET /v1/apps/{app}/logs", ScopeRead, logs.logs)
	route(mux, "POST /v1/apps/{app}/deployments", ScopeDeploy, ops.deploy)
	route(mux, "GET /v1/apps/{app}/deployments", ScopeRead, ops.releases)
	route(mux, "POST /v1/apps/{app}/rollbacks", ScopeDeploy, ops.rollback)
	route(mux, "GET /v1/operations/{id}", ScopeRead, ops.get)
	route(mux, "GET /v1/operations/{id}/events", ScopeRead, ops.events)
	return mux, routes
}

// handleWhoami describes the calling token, so the CLI can check its
// configuration without side effects.
func handleWhoami(w http.ResponseWriter, r *http.Request) {
	tok := principal(r.Context())
	writeJSON(w, http.StatusOK, "application/json", struct {
		Token     string     `json:"token"`
		Name      string     `json:"name"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at"`
	}{tok.Prefix, tok.Name, tok.Scopes, tok.ExpiresAt})
}

// handleHealthz reports liveness: the process is up and serving HTTP.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}

// handleReadyz reports readiness: dependencies needed to serve requests work.
func handleReadyz(log *slog.Logger, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			log.WarnContext(r.Context(), "readiness check failed", slog.String("dependency", "postgres"), slog.Any("err", err))
			writeProblem(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ready"})
	}
}
