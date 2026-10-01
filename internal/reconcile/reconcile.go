// Package reconcile makes Docker and Caddy match PostgreSQL, at worker start
// and then periodically (ARCHITECTURE §5, Reconciler; invariant 2). Every
// step is idempotent and carries on past a failing item; a pass never
// stops the worker.
package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// Queue returns expired operations to the queue (store.RequeueExpired).
type Queue interface {
	RequeueExpired(ctx context.Context) (requeued, failed int, err error)
}

// Store is what restoring active containers reads and records.
type Store interface {
	ActiveDeployments(ctx context.Context) ([]store.Deployment, error)
	AppByID(ctx context.Context, id string) (store.App, error)
	ReplaceContainer(ctx context.Context, id, old, container string) error
	EnqueueRebuild(ctx context.Context, deploymentID string, payload json.RawMessage) (store.Operation, bool, error)
}

// Runtime inspects and recreates containers (internal/runtime). Inspect
// reports a missing container with app.ErrContainerGone.
type Runtime interface {
	Inspect(ctx context.Context, id string) (app.ContainerState, error)
	ImageExists(ctx context.Context, id string) (bool, error)
	Create(ctx context.Context, c app.Container) (string, error)
	Start(ctx context.Context, id string) error
}

// Env decrypts a deployment's environment revision (internal/secrets).
type Env interface {
	Resolve(ctx context.Context, revisionID string) (map[string]string, error)
}

// Router loads the routes table into Caddy (routing.Router).
type Router interface {
	Sync(ctx context.Context) (bool, error)
}

// Sweeper drains and removes containers that should no longer run
// (app.Janitor).
type Sweeper interface {
	Sweep(ctx context.Context, now time.Time) ([]string, error)
}

// Pruner removes images retention no longer keeps (app.ImagePruner).
type Pruner interface {
	Prune(ctx context.Context) ([]string, error)
}

// Reconciler runs the steps; Router is nil when Caddy is disabled. A nil
// Images skips image retention.
type Reconciler struct {
	Queue   Queue
	Store   Store
	Runtime Runtime
	Env     Env
	Router  Router
	Janitor Sweeper
	Images  Pruner
	Log     *slog.Logger
}

// Run does a pass at once and then every interval, until ctx ends.
func (r *Reconciler) Run(ctx context.Context, every time.Duration) { Every(ctx, every, r.Pass) }

// Every calls fn at once and then every interval, until ctx ends. A slow
// call delays the next one rather than overlapping it.
func Every(ctx context.Context, every time.Duration, fn func(context.Context)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		fn(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Pass runs every step once, in order:
//  1. return operations of crashed workers to the queue, or fail them with
//     their deployment on the last attempt;
//  2. restore active deployments whose container is gone or stopped;
//  3. make Caddy serve the routes table (a switch in progress is kept);
//  4. drain superseded containers and remove failed ones (app.Janitor),
//     after the sync, so Caddy no longer points at what it stops;
//  5. remove images retention no longer keeps (ADR-0006), after the
//     cleanup, so the containers that used them are gone.
func (r *Reconciler) Pass(ctx context.Context) {
	warn := func(msg string, err error) {
		if ctx.Err() == nil {
			r.Log.Warn(msg, slog.Any("err", err))
		}
	}
	requeued, failed, err := r.Queue.RequeueExpired(ctx)
	switch {
	case err != nil:
		warn("requeue expired operations failed", err)
	case requeued+failed > 0:
		r.Log.Info("expired operations requeued", slog.Int("requeued", requeued), slog.Int("failed", failed))
	}
	if err := r.restoreActive(ctx); err != nil {
		warn("restoring active containers incomplete", err)
	}
	if r.Router != nil {
		changed, err := r.Router.Sync(ctx)
		switch {
		case err != nil:
			warn("caddy sync failed", err)
		case changed:
			r.Log.Info("caddy config reloaded from the routes table")
		}
	}
	if removed, err := r.Janitor.Sweep(ctx, time.Now()); err != nil {
		warn(fmt.Sprintf("container cleanup incomplete (%d removed)", len(removed)), err)
	}
	if r.Images != nil {
		if removed, err := r.Images.Prune(ctx); err != nil {
			warn(fmt.Sprintf("image retention incomplete (%d removed)", len(removed)), err)
		}
	}
}

// restoreActive brings back the container of every active deployment
// (ARCHITECTURE §5, Reconciler step 2). A stopped one is started. A missing
// one is recreated from the deployment's image ID and environment revision,
// with the same deterministic name, so its routes resolve again without a
// Caddy change; the new ID is recorded before the start (invariant 3). No
// health gate runs: this is the release that already passed one, from the
// same image and configuration (invariants 5 and 6). When the image is gone
// as well, the commit is rebuilt by a deploy, health gate included (rebuild).
func (r *Reconciler) restoreActive(ctx context.Context) error {
	deps, err := r.Store.ActiveDeployments(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range deps {
		if err := r.restore(ctx, d); err != nil {
			errs = append(errs, fmt.Errorf("deployment %s: %w", d.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (r *Reconciler) restore(ctx context.Context, d store.Deployment) error {
	log := r.Log.With(slog.String("app_id", d.AppID), slog.String("deployment_id", d.ID))
	st, err := r.Runtime.Inspect(ctx, d.ContainerID)
	switch {
	case err == nil && (st.Running || st.Restarting):
		return nil // serving, or Docker's restart policy is at work
	case err == nil:
		log.Warn("active container is stopped; starting it", slog.String("container_id", d.ContainerID))
		return r.Runtime.Start(ctx, d.ContainerID)
	case !errors.Is(err, app.ErrContainerGone):
		return err
	}

	// Without the image too (a restore onto a fresh host, ADR-0006, or a
	// manual prune), the commit is built again by an ordinary deploy.
	switch ok, err := r.Runtime.ImageExists(ctx, d.ImageID); {
	case err != nil:
		return fmt.Errorf("check image %s: %w", d.ImageID, err)
	case !ok:
		return r.rebuild(ctx, d, log)
	}

	a, err := r.Store.AppByID(ctx, d.AppID)
	if err != nil {
		return err
	}
	env := map[string]string{}
	if d.EnvRevisionID != nil {
		if env, err = r.Env.Resolve(ctx, *d.EnvRevisionID); err != nil {
			return fmt.Errorf("resolve environment: %w", err)
		}
	}
	id, err := r.Runtime.Create(ctx, app.Container{App: a.Slug, DeploymentID: d.ID, Commit: d.SourceCommitSHA,
		Image: d.ImageID, Env: env, CPUs: a.CPULimit, Memory: a.MemoryLimit, StopTimeout: a.StopTimeout})
	if err != nil {
		return fmt.Errorf("recreate from image %s: %w", d.ImageID, err)
	}
	if err := r.Store.ReplaceContainer(ctx, d.ID, d.ContainerID, id); err != nil {
		// Superseded meanwhile: the janitor removes the new container.
		return fmt.Errorf("record recreated container: %w", err)
	}
	if err := r.Runtime.Start(ctx, id); err != nil {
		return fmt.Errorf("start recreated container: %w", err)
	}
	log.Warn("active container was gone; recreated it", slog.String("old_container_id", d.ContainerID),
		slog.String("container_id", id), slog.String("image_id", d.ImageID))
	return nil
}

// rebuild asks, once per deployment, for a deploy of its commit with its
// environment revision. While that deploy is queued or running there is
// nothing more to do. If it failed, only an operator's deploy or rollback
// helps, and every pass says so. An app with another operation in progress
// is left to that operation.
func (r *Reconciler) rebuild(ctx context.Context, d store.Deployment, log *slog.Logger) error {
	payload, err := json.Marshal(app.DeployPayload{RebuildOf: d.ID})
	if err != nil {
		return err
	}
	op, created, err := r.Store.EnqueueRebuild(ctx, d.ID, payload)
	switch {
	case errors.Is(err, store.ErrConflict):
		return nil
	case err != nil:
		return fmt.Errorf("request rebuild: %w", err)
	case created:
		log.Warn("active container and image are gone; rebuilding the commit", slog.String("operation_id", op.ID),
			slog.String("commit", d.SourceCommitSHA), slog.String("image_id", d.ImageID))
		return nil
	case op.Status == store.OpQueued || op.Status == store.OpRunning:
		return nil
	}
	return fmt.Errorf("image %s is gone and its rebuild (operation %s) %s: deploy commit %s or roll back to recover",
		d.ImageID, op.ID, op.Status, d.SourceCommitSHA)
}
