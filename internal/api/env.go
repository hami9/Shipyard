package api

import (
	"context"
	"net/http"
	"regexp"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/store"
)

// EnvStore manages environment revisions; *secrets.Env implements it. The
// API only seals and lists; it never decrypts (ADR-0005).
type EnvStore interface {
	Set(ctx context.Context, appID, key string, value []byte, secret bool) (store.EnvRevision, error)
	Unset(ctx context.Context, appID, key string) (store.EnvRevision, error)
	Keys(ctx context.Context, appID string) (int, []secrets.Var, error)
}

// envKeyRE mirrors the CHECK on secret_values.key and env_revision_entries.key.
var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,254}$`)

type envHandlers struct {
	*appHandlers
	env EnvStore
}

type envVarJSON struct {
	Key    string `json:"key"`
	Secret bool   `json:"secret"`
}

// envJSON lists keys and whether each is secret, never values.
type envJSON struct {
	Revision int          `json:"revision"`
	Vars     []envVarJSON `json:"vars"`
}

func (h *envHandlers) respond(w http.ResponseWriter, r *http.Request, appID string) {
	n, vars, err := h.env.Keys(r.Context(), appID)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	out := envJSON{Revision: n, Vars: make([]envVarJSON, len(vars))}
	for i, v := range vars {
		out.Vars[i] = envVarJSON{v.Key, v.Secret}
	}
	writeJSON(w, http.StatusOK, "application/json", out)
}

// target resolves {app} and validates {key}.
func (h *envHandlers) target(w http.ResponseWriter, r *http.Request) (appID, key string, ok bool) {
	key = r.PathValue("key")
	if !envKeyRE.MatchString(key) {
		writeError(w, r, h.log, app.FieldErrors{{Field: "key", Detail: "must be a shell-style name: a letter or '_', then letters, digits, or '_'"}})
		return "", "", false
	}
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return "", "", false
	}
	return a.ID, key, true
}

func (h *envHandlers) list(w http.ResponseWriter, r *http.Request) {
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	h.respond(w, r, a.ID)
}

// set stores a value; it is secret unless the body says "secret": false.
// The value is never echoed or logged.
func (h *envHandlers) set(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Value  *string `json:"value"`
		Secret *bool   `json:"secret"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	if req.Value == nil {
		writeError(w, r, h.log, app.FieldErrors{{Field: "value", Detail: "is required"}})
		return
	}
	appID, key, ok := h.target(w, r)
	if !ok {
		return
	}
	secret := req.Secret == nil || *req.Secret
	if _, err := h.env.Set(r.Context(), appID, key, []byte(*req.Value), secret); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	h.respond(w, r, appID)
}

func (h *envHandlers) unset(w http.ResponseWriter, r *http.Request) {
	appID, key, ok := h.target(w, r)
	if !ok {
		return
	}
	if _, err := h.env.Unset(r.Context(), appID, key); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	h.respond(w, r, appID)
}
