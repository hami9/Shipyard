package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/hami9/shipyard/internal/store"
)

// prepareRebuild checks the deployment a rebuild replaces and returns its
// commit. The rebuild is an ordinary deploy of that commit: the fetch still
// requires it on the tracked branch (invariant 7), and only a healthy
// candidate takes the traffic (invariant 5). It pins the environment
// revision the deployment ran with, so the app comes back as it was.
//
// A deployment that is no longer active needs no rebuild: something else
// replaced it while this operation waited, and building its commit now would
// move the app backwards.
func (r *deployRun) prepareRebuild(ctx context.Context, id string) (string, error) {
	d, err := r.Store.DeploymentByID(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "", fmt.Errorf("deployment %s to rebuild does not exist", id)
	case err != nil:
		return "", fmt.Errorf("load deployment to rebuild: %w", err)
	case d.AppID != r.app.ID:
		return "", fmt.Errorf("deployment %s is not a deployment of this app", id)
	case d.Status != store.DeployActive:
		return "", fmt.Errorf("deployment %s is %s, no longer active: nothing to rebuild", id, d.Status)
	}
	r.rebuild = &d
	r.event(ctx, store.LevelWarn, "rebuilding deployment %s (commit %s): its image %s is gone from this host", d.ID, d.SourceCommitSHA, d.ImageID)
	return d.SourceCommitSHA, nil
}
