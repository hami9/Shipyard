package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hami9/shipyard/internal/logging"
	"github.com/hami9/shipyard/internal/store"
)

// TokenStore is what authentication needs from persistence.
type TokenStore interface {
	ActiveTokenByHash(ctx context.Context, hash []byte) (store.Token, error)
	TouchToken(ctx context.Context, id string) error
}

// AuditRecorder appends audit events.
type AuditRecorder interface {
	RecordAudit(ctx context.Context, e store.AuditEvent) (store.AuditEvent, error)
}

const realm = `Bearer realm="shipyard"`

type principalKey struct{}

// principal returns the authenticated token; protect guarantees it exists.
func principal(ctx context.Context) store.Token {
	t, _ := ctx.Value(principalKey{}).(store.Token)
	return t
}

type authenticator struct {
	log    *slog.Logger
	tokens TokenStore
	audit  AuditRecorder
}

// protect requires a bearer token with the given scope [RFC6750], and
// records an audit event for every mutation, allowed or denied (ADR-0007).
// Anonymous failures are logged but not audited, so unauthenticated clients
// cannot fill the audit table.
func (a *authenticator) protect(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		ctx := context.WithValue(r.Context(), principalKey{}, tok)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		if hasScope(tok.Scopes, scope) {
			next.ServeHTTP(rec, r.WithContext(ctx))
		} else {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`%s, error="insufficient_scope", scope=%q`, realm, scope))
			writeProblem(rec, http.StatusForbidden, "this token lacks the "+scope+" scope")
		}
		if isMutation(r.Method) {
			a.record(ctx, r, tok, rec.status)
		}
	})
}

// authenticate writes a 401 or 503 and returns false when the request has no
// active token. Malformed, unknown, expired, and revoked tokens look the same
// to the client.
func (a *authenticator) authenticate(w http.ResponseWriter, r *http.Request) (store.Token, bool) {
	scheme, raw, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		// No credentials, or another scheme: no error details [RFC6750 §3.1].
		w.Header().Set("WWW-Authenticate", realm)
		writeProblem(w, http.StatusUnauthorized, "a bearer token is required")
		return store.Token{}, false
	}
	raw = strings.TrimSpace(raw)
	var tok store.Token
	err := store.ErrNotFound
	if wellFormedToken(raw) {
		tok, err = a.tokens.ActiveTokenByHash(r.Context(), HashToken(raw))
	}
	if errors.Is(err, store.ErrNotFound) {
		w.Header().Set("WWW-Authenticate", realm+`, error="invalid_token"`)
		writeProblem(w, http.StatusUnauthorized, "the token is invalid, expired, or revoked")
		return store.Token{}, false
	}
	if err != nil {
		a.log.ErrorContext(r.Context(), "token lookup failed", slog.Any("err", err))
		writeProblem(w, http.StatusServiceUnavailable, "authentication is temporarily unavailable")
		return store.Token{}, false
	}
	if err := a.tokens.TouchToken(r.Context(), tok.ID); err != nil {
		a.log.WarnContext(r.Context(), "token last-used update failed", slog.String("token", tok.Prefix), slog.Any("err", err))
	}
	return tok, true
}

func isMutation(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// record writes the audit event after the handler ran. The write outlives a
// client disconnect, and a failure is logged loudly rather than hidden.
func (a *authenticator) record(ctx context.Context, r *http.Request, tok store.Token, status int) {
	result := store.AuditSuccess
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		result = store.AuditDenied
	case status >= 400:
		result = store.AuditFailure
	}
	target := r.URL.Path
	if len(target) > 255 {
		target = target[:255]
	}
	ctx = context.WithoutCancel(ctx)
	_, err := a.audit.RecordAudit(ctx, store.AuditEvent{
		Actor:     "token:" + tok.Prefix,
		Action:    r.Pattern, // the route, e.g. "POST /v1/apps/{id}/deployments"
		Target:    target,
		Result:    result,
		RequestID: logging.Get(ctx, logging.RequestID),
	})
	if err != nil {
		a.log.ErrorContext(ctx, "audit event not recorded", slog.String("action", r.Pattern), slog.Any("err", err))
	}
}
