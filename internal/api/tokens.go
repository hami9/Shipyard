package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// TokenAdmin is what the token routes need from persistence (ADR-0011).
type TokenAdmin interface {
	ListTokens(ctx context.Context) ([]store.Token, error)
	RevokeToken(ctx context.Context, prefix string) (store.Token, error)
	RotateToken(ctx context.Context, oldID string, n store.NewToken, grace time.Duration) (old, created store.Token, err error)
}

// prefixRE matches the api_tokens.prefix check constraint.
var prefixRE = regexp.MustCompile(`^shp_[A-Za-z0-9_-]{4,16}$`)

type tokenHandlers struct {
	log    *slog.Logger
	tokens TokenAdmin
	now    func() time.Time
}

// tokenView is a token's metadata as the API shows it: never its hash.
type tokenView struct {
	Prefix     string     `json:"prefix"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Status     string     `json:"status"` // active, expired, or revoked
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func viewToken(t store.Token, now time.Time) tokenView {
	status := "active"
	switch {
	case t.RevokedAt != nil:
		status = "revoked"
	case t.ExpiresAt != nil && !t.ExpiresAt.After(now):
		status = "expired"
	}
	return tokenView{Prefix: t.Prefix, Name: t.Name, Scopes: t.Scopes, Status: status,
		ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt}
}

// list is GET /v1/tokens (admin): every token, newest first.
func (h *tokenHandlers) list(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.tokens.ListTokens(r.Context())
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	now := h.now()
	views := make([]tokenView, len(tokens))
	for i, t := range tokens {
		views[i] = viewToken(t, now)
	}
	writeJSON(w, http.StatusOK, "application/json", map[string]any{"tokens": views})
}

// revoke is DELETE /v1/tokens/{prefix} (admin). Revoking a revoked token
// is a no-op; revoking the calling token is allowed.
func (h *tokenHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	if !prefixRE.MatchString(prefix) {
		writeProblem(w, http.StatusNotFound, "no such token")
		return
	}
	t, err := h.tokens.RevokeToken(r.Context(), prefix)
	if errors.Is(err, store.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "no such token")
		return
	}
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	writeJSON(w, http.StatusOK, "application/json", viewToken(t, h.now()))
}

// rotateRequest is the optional body of POST /v1/tokens/self/rotate.
type rotateRequest struct {
	GraceSeconds int64 `json:"grace_seconds"`
}

// rotateResponse carries the new token's plaintext: the only time it is
// shown.
type rotateResponse struct {
	Token string    `json:"token"`
	New   tokenView `json:"new"`
	Old   tokenView `json:"old"`
}

// rotateSelf is POST /v1/tokens/self/rotate (any scope): it replaces the
// calling token with one of the same user, name, scopes, and lifetime, and
// ends the old one now or after grace_seconds (ADR-0011).
func (h *tokenHandlers) rotateSelf(w http.ResponseWriter, r *http.Request) {
	var req rotateRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, r, h.log, err)
			return
		}
	}
	if req.GraceSeconds < 0 || req.GraceSeconds > int64(MaxRotationGrace/time.Second) {
		writeError(w, r, h.log, app.FieldErrors{{Field: "grace_seconds", Detail: "must be between 0 and 604800 (7 days)"}})
		return
	}
	cur := principal(r.Context())
	now := h.now()
	plaintext, n := Rotation(cur, now)
	old, tok, err := h.tokens.RotateToken(r.Context(), cur.ID, n, time.Duration(req.GraceSeconds)*time.Second)
	if errors.Is(err, store.ErrNotFound) {
		// Revoked or expired since this request authenticated.
		writeProblem(w, http.StatusConflict, "this token is no longer active")
		return
	}
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	h.log.InfoContext(r.Context(), "token rotated", slog.String("token", old.Prefix), slog.String("new_token", tok.Prefix))
	// The body is a credential: no cache may keep it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, "application/json", rotateResponse{Token: plaintext, New: viewToken(tok, now), Old: viewToken(old, now)})
}
