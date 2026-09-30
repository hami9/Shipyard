package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hami9/shipyard/internal/store"
)

// RollbackPayload is a rollback operation's payload (ARCHITECTURE §5,
// Rollback).
type RollbackPayload struct {
	Target string `json:"target"` // the earlier deployment to run again
	// WithCurrentConfig runs the target's image with the app's latest
	// environment revision instead of the one it ran with.
	WithCurrentConfig bool `json:"with_current_config,omitempty"`
}

// RotatedSecrets names the secret keys of revision old whose value is no
// longer the app's: set to another value, or removed, in revision latest.
// Rolling back with old would bring those values back. Plain keys and keys
// added since do not count. Keys are sorted, as revision entries are.
func RotatedSecrets(old, latest []store.EnvEntry) []string {
	now := make(map[string]*string, len(latest))
	for _, e := range latest {
		now[e.Key] = e.SecretValueID
	}
	var out []string
	for _, e := range old {
		if e.SecretValueID == nil {
			continue
		}
		if cur, ok := now[e.Key]; !ok || cur == nil || *cur != *e.SecretValueID {
			out = append(out, e.Key)
		}
	}
	return out
}

// ErrRollbackUnavailable means the target's image is no longer on the host:
// a rollback never rebuilds from a moving branch (invariant 6).
var ErrRollbackUnavailable = errors.New("rollback unavailable")

// prepareRollback creates the rollback's deployment from its target: the
// same commit and image ID, and the target's environment revision (or the
// latest one). It checks first that the image is still on the host, so an
// unavailable rollback fails before any side effect.
func (r *deployRun) prepareRollback(ctx context.Context) error {
	var p RollbackPayload
	if err := json.Unmarshal(r.op.Payload, &p); err != nil || p.Target == "" {
		return fmt.Errorf("invalid rollback payload: %v", err)
	}
	src, err := r.Store.DeploymentByID(ctx, p.Target)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("rollback target %s does not exist", p.Target)
	case err != nil:
		return fmt.Errorf("load rollback target: %w", err)
	case src.AppID != r.app.ID || src.ImageID == "":
		return fmt.Errorf("deployment %s is not a built deployment of this app", p.Target)
	}
	ok, err := r.Runtime.ImageExists(ctx, src.ImageID)
	if err != nil {
		return fmt.Errorf("check image %s: %w", src.ImageID, err)
	}
	if !ok {
		return fmt.Errorf("%w: image %s of deployment %s is no longer on this host; deploy commit %s again instead",
			ErrRollbackUnavailable, src.ImageID, src.ID, src.SourceCommitSHA)
	}
	rev := src.EnvRevisionID
	if p.WithCurrentConfig {
		latest, err := r.Store.LatestEnvRevision(ctx, r.app.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			rev = nil
		case err != nil:
			return fmt.Errorf("load environment: %w", err)
		default:
			rev = &latest.ID
		}
	}
	if r.dep, err = r.Store.CreateRollbackDeployment(ctx, r.owner, store.NewRollback{OperationID: r.op.ID, Source: src, EnvRevisionID: rev}); err != nil {
		return fmt.Errorf("record rollback deployment: %w", err)
	}
	config := "the configuration it ran with"
	if p.WithCurrentConfig {
		config = "the current configuration"
	}
	r.event(ctx, store.LevelInfo, "rolling back to deployment %s (commit %s, image %s) with %s", src.ID, src.SourceCommitSHA, src.ImageID, config)
	return nil
}
