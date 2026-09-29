// Package api implements shipyard-api's HTTP surface. The API validates
// requests and records intent in PostgreSQL; it never touches Docker, Caddy,
// or repository code (CLAUDE.md invariant 1).
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"
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
	Audit  AuditRecorder
	Apps   AppStore
	Env    EnvStore
	Ops    OperationStore
	// Domains, Resolver, and DomainPolicy serve /v1/apps/{app}/domains.
	Domains      DomainStore
	Resolver     Resolver
	DomainPolicy DomainPolicy
	// StreamPoll and StreamKeepalive tune event and log streams; zero means
	// the defaults (tests shorten them).
	StreamPoll, StreamKeepalive time.Duration
	// Logs reads app logs from the worker (ADR-0008); nil answers 503.
	Logs LogSource
}

// NewHandler returns the root handler with middleware applied. Health
// endpoints are public; every /v1 route goes through auth.protect.
func NewHandler(log *slog.Logger, d Deps) http.Handler {
	auth := &authenticator{log: log, tokens: d.Tokens, audit: d.Audit}
	route := func(mux *http.ServeMux, pattern, scope string, h http.HandlerFunc) {
		mux.Handle(pattern, auth.protect(scope, h))
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz(log, d.DB))
	route(mux, "GET /v1/whoami", ScopeRead, handleWhoami)
	route(mux, "GET /v1/apps", ScopeRead, apps.list)
	route(mux, "POST /v1/apps", ScopeAdmin, apps.create)
	route(mux, "GET /v1/apps/{app}", ScopeRead, apps.get)
	route(mux, "PATCH /v1/apps/{app}", ScopeAdmin, apps.update)
	route(mux, "DELETE /v1/apps/{app}", ScopeAdmin, apps.delete)
	route(mux, "GET /v1/apps/{app}/env", ScopeRead, env.list)
	route(mux, "PUT /v1/apps/{app}/env/{key}", ScopeAdmin, env.set)
	route(mux, "DELETE /v1/apps/{app}/env/{key}", ScopeAdmin, env.unset)
	route(mux, "GET /v1/apps/{app}/domains", ScopeRead, domains.list)
	route(mux, "POST /v1/apps/{app}/domains", ScopeAdmin, domains.add)
	route(mux, "DELETE /v1/apps/{app}/domains/{hostname}", ScopeAdmin, domains.remove)
	route(mux, "GET /v1/apps/{app}/logs", ScopeRead, logs.logs)
	route(mux, "POST /v1/apps/{app}/deployments", ScopeDeploy, ops.deploy)
	route(mux, "GET /v1/apps/{app}/deployments", ScopeRead, ops.releases)
	route(mux, "GET /v1/operations/{id}", ScopeRead, ops.get)
	route(mux, "GET /v1/operations/{id}/events", ScopeRead, ops.events)
	return withRequestID(withAccessLog(log, mux))
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
