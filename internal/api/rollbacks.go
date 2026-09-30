package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// rollback admits a rollback to an earlier deployment (ARCHITECTURE §5,
// Rollback). Only a deployment that served before (superseded) is a
// target. When secrets have changed since it ran, the operator must choose
// between the values it ran with and today's (409 otherwise). Whether its
// image is still on the host is the worker's check (invariant 1): an
// unavailable rollback fails at once, before any side effect.
func (h *opHandlers) rollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To                *string `json:"to"`
		WithCurrentConfig bool    `json:"with_current_config"`
		WithOldConfig     bool    `json:"with_old_config"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	var fe app.FieldErrors
	if body.To == nil || !uuidRE.MatchString(*body.To) {
		fe = append(fe, app.FieldError{Field: "to", Detail: "must be a deployment ID"})
	}
	if body.WithCurrentConfig && body.WithOldConfig {
		fe = append(fe, app.FieldError{Field: "with_old_config", Detail: "cannot be combined with with_current_config"})
	}
	key, fe := idempotencyKey(r, fe)
	if len(fe) > 0 {
		writeError(w, r, h.log, fe)
		return
	}
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	ctx := r.Context()
	target, err := h.ops.DeploymentByID(ctx, *body.To)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && target.AppID != a.ID):
		writeError(w, r, h.log, app.FieldErrors{{Field: "to", Detail: "is not a deployment of this app"}})
		return
	case err != nil:
		writeError(w, r, h.log, err)
		return
	case target.Status == store.DeployActive:
		writeProblem(w, http.StatusConflict, "deployment "+target.ID+" is already active")
		return
	case target.Status != store.DeploySuperseded:
		writeError(w, r, h.log, app.FieldErrors{{Field: "to", Detail: "never served traffic (status " + target.Status + "); only a superseded deployment is a rollback target"}})
		return
	}
	if !body.WithCurrentConfig && !body.WithOldConfig {
		rotated, err := h.rotatedSecrets(ctx, a.ID, target)
		if err != nil {
			writeError(w, r, h.log, err)
			return
		}
		if len(rotated) > 0 {
			writeProblem(w, http.StatusConflict, "secrets changed since this deployment: "+strings.Join(rotated, ", ")+
				". Retry with with_current_config (today's values) or with_old_config (the values it ran with)")
			return
		}
	}
	payload, _ := json.Marshal(app.RollbackPayload{Target: target.ID, WithCurrentConfig: body.WithCurrentConfig})
	h.admit(w, r, a, app.KindRollback, key, payload)
}

// rotatedSecrets names the target revision's secret keys whose values are
// no longer the app's. Only keys are compared, never values.
func (h *opHandlers) rotatedSecrets(ctx context.Context, appID string, target store.Deployment) ([]string, error) {
	if target.EnvRevisionID == nil {
		return nil, nil
	}
	old, err := h.ops.EnvRevisionByID(ctx, *target.EnvRevisionID)
	if err != nil {
		return nil, err
	}
	latest, err := h.ops.LatestEnvRevision(ctx, appID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return app.RotatedSecrets(old.Entries, latest.Entries), nil
}
