package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// JanitorStore is what the janitor reads.
type JanitorStore interface {
	DeploymentByID(ctx context.Context, id string) (store.Deployment, error)
	AppByID(ctx context.Context, id string) (store.App, error)
}

// ManagedContainer is an app container Shipyard created, from its labels.
type ManagedContainer struct {
	ID, App, DeploymentID string
	Running               bool
}

// JanitorRuntime lists and removes app containers (internal/runtime).
type JanitorRuntime interface {
	ListManaged(ctx context.Context) ([]ManagedContainer, error)
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Remove(ctx context.Context, id string) error
}

// Janitor is Reconciler step 2 for containers that should no longer run
// (ARCHITECTURE §5): the database decides, Docker is derived state
// (invariant 2). It drains a superseded deployment once its observation
// window has passed: a graceful stop with the app's stop_timeout (SIGTERM,
// then SIGKILL), then removal. It removes containers of failed or cancelled
// deployments (a candidate a crash left behind). Active and in-progress
// deployments are left alone; the deploy that owns them decides.
//
// A container whose deployment is not in this database is never touched:
// deployment rows are kept, so it belongs to another Shipyard database on
// the same engine (a test run, a development worker), and Shipyard removes
// only what it can show it owns.
type Janitor struct {
	Store   JanitorStore
	Runtime JanitorRuntime
	Window  time.Duration
	Log     *slog.Logger
}

// Sweep runs one pass and reports what it removed. It carries on past a
// failed container and returns the errors joined.
func (j *Janitor) Sweep(ctx context.Context, now time.Time) (removed []string, err error) {
	containers, err := j.Runtime.ListManaged(ctx)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, c := range containers {
		stop, reason, err := j.decide(ctx, c, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("container %s: %w", c.ID[:min(12, len(c.ID))], err))
			continue
		}
		if reason == "" {
			continue
		}
		if stop >= 0 && c.Running {
			if err := j.Runtime.Stop(ctx, c.ID, stop); err != nil {
				errs = append(errs, fmt.Errorf("stop %s: %w", c.ID[:min(12, len(c.ID))], err))
				continue
			}
		}
		if err := j.Runtime.Remove(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", c.ID[:min(12, len(c.ID))], err))
			continue
		}
		j.Log.Info("container removed", slog.String("container_id", c.ID), slog.String("app", c.App),
			slog.String("deployment_id", c.DeploymentID), slog.String("reason", reason))
		removed = append(removed, c.ID)
	}
	return removed, errors.Join(errs...)
}

// decide returns why the container goes (empty: it stays) and the graceful
// stop timeout to use first (-1: remove at once).
func (j *Janitor) decide(ctx context.Context, c ManagedContainer, now time.Time) (time.Duration, string, error) {
	d, err := j.Store.DeploymentByID(ctx, c.DeploymentID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return 0, "", nil // not this installation's: leave it
	case err != nil:
		return 0, "", err
	}
	switch d.Status {
	case store.DeployFailed, store.DeployCancelled:
		return -1, "its deployment " + d.Status, nil
	case store.DeploySuperseded:
		if d.EndedAt == nil || now.Before(d.EndedAt.Add(j.Window)) {
			return 0, "", nil // still in its observation window
		}
		a, err := j.Store.AppByID(ctx, d.AppID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return -1, "superseded, and its app no longer exists", nil
		case err != nil:
			return 0, "", err
		}
		return a.StopTimeout, "superseded, observation window over", nil
	}
	return 0, "", nil
}
