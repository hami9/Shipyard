package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// KindDelete is the operation that removes an app (P3.8).
const KindDelete = store.KindDelete

// Delete phases, persisted before each step (invariant 3).
const (
	PhaseRelease    = "release"    // out of service: routes, Caddy
	PhaseContainers = "containers" // stop and remove
	PhaseNetwork    = "network"
	PhaseImages     = "images"
	PhaseRemove     = "remove" // the app row and, by cascade, the rest
)

// AuditAppDelete is the audit action of a finished or failed app delete.
const AuditAppDelete = "app.delete"

// DeleteStore is what the delete use case needs from persistence.
type DeleteStore interface {
	AppByID(ctx context.Context, id string) (store.App, error)
	SetOperationPhase(ctx context.Context, id, owner, phase string) error
	FailOperation(ctx context.Context, id, owner, reason string, retryAfter time.Duration) (string, error)
	AppendOperationEvent(ctx context.Context, opID, level, message string) (store.OperationEvent, error)
	StopServing(ctx context.Context, appID, opID, owner string) ([]string, error)
	AppArtifacts(ctx context.Context, appID string) (store.Artifacts, error)
	FinishAppDelete(ctx context.Context, appID, opID, owner string, audit store.AuditEvent) error
	RecordAudit(ctx context.Context, e store.AuditEvent) (store.AuditEvent, error)
}

// DeleteRuntime removes what the app has on the host (internal/runtime).
type DeleteRuntime interface {
	ListManaged(ctx context.Context) ([]ManagedContainer, error)
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Remove(ctx context.Context, id string) error
	RemoveNetwork(ctx context.Context, app string) error
	ListImages(ctx context.Context) ([]string, error)
	RemoveImage(ctx context.Context, id string) error
}

// RouteSyncer loads the routes table into Caddy (routing.Router).
type RouteSyncer interface {
	Sync(ctx context.Context) (bool, error)
}

// Deleter runs delete operations (P3.8): it takes an app out of service
// and removes everything it has, in an order that never leaves traffic on
// something half gone:
//
//  1. release: the routes leave the database and Caddy; the deployments
//     stop being active, so the reconciler restores nothing;
//  2. containers: stopped gracefully (the app's stop_timeout), then removed;
//  3. network: the app's bridge network;
//  4. images: every image a deployment of the app recorded;
//  5. remove: the app row, whose cascade takes its environment, secrets,
//     history, and this operation. An audit event stays.
//
// Every step is idempotent, so a retry after a crash starts over safely.
// Only containers and images that this database recorded for the app are
// touched (P2.6).
type Deleter struct {
	Store   DeleteStore
	Runtime DeleteRuntime
	// Router is nil when Caddy is disabled.
	Router RouteSyncer
	Log    *slog.Logger
}

// Run executes a claimed delete operation. Like Deployer.Run, it returns
// nil once the outcome is recorded (the app is gone, or the operation
// failed with a reason) and an error when it was interrupted and resumes
// after its lease expires.
func (d *Deleter) Run(ctx context.Context, op store.Operation, owner string) error {
	r := &deleteRun{Deleter: d, op: op, owner: owner,
		log: d.Log.With(slog.String("operation_id", op.ID), slog.String("app_id", op.AppID))}
	err := r.execute(ctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrLeaseLost) || ctx.Err() != nil {
		r.log.Warn("delete interrupted; it resumes after the lease expires", slog.Any("err", err))
		return err
	}
	return r.fail(ctx, err)
}

type deleteRun struct {
	*Deleter
	op    store.Operation
	owner string
	log   *slog.Logger
	app   store.App
}

func (r *deleteRun) execute(ctx context.Context) error {
	var err error
	if r.app, err = r.Store.AppByID(ctx, r.op.AppID); err != nil {
		return fmt.Errorf("load app: %w", err)
	}
	if r.op.Attempt > 1 {
		r.event(ctx, store.LevelInfo, "resuming the delete of %s (attempt %d)", r.app.Slug, r.op.Attempt)
	}

	if err := r.phase(ctx, PhaseRelease); err != nil {
		return err
	}
	hosts, err := r.Store.StopServing(ctx, r.app.ID, r.op.ID, r.owner)
	if err != nil {
		return fmt.Errorf("take out of service: %w", err)
	}
	if r.Router != nil {
		// Before any container stops, so Caddy never points at one that is gone.
		if _, err := r.Router.Sync(ctx); err != nil {
			return fmt.Errorf("remove routes from caddy: %w", err)
		}
	}
	if len(hosts) > 0 {
		r.event(ctx, store.LevelInfo, "out of service: %s", strings.Join(hosts, ", "))
	}

	if err := r.phase(ctx, PhaseContainers); err != nil {
		return err
	}
	art, err := r.Store.AppArtifacts(ctx, r.app.ID)
	if err != nil {
		return fmt.Errorf("list deployments: %w", err)
	}
	containers, err := r.Runtime.ListManaged(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	removed := 0
	for _, c := range containers {
		if c.App != r.app.Slug || !slices.Contains(art.Deployments, c.DeploymentID) {
			continue // not this app's, or not this installation's
		}
		if c.Running {
			if err := r.Runtime.Stop(ctx, c.ID, r.app.StopTimeout); err != nil {
				return fmt.Errorf("stop container %.12s: %w", c.ID, err)
			}
		}
		if err := r.Runtime.Remove(ctx, c.ID); err != nil {
			return fmt.Errorf("remove container %.12s: %w", c.ID, err)
		}
		removed++
	}
	r.event(ctx, store.LevelInfo, "%d containers removed", removed)

	if err := r.phase(ctx, PhaseNetwork); err != nil {
		return err
	}
	if err := r.Runtime.RemoveNetwork(ctx, r.app.Slug); err != nil {
		return fmt.Errorf("remove network: %w", err)
	}

	if err := r.phase(ctx, PhaseImages); err != nil {
		return err
	}
	present, err := r.Runtime.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	images := 0
	for _, id := range art.Images {
		if !slices.Contains(present, id) {
			continue
		}
		switch err := r.Runtime.RemoveImage(ctx, id); {
		case errors.Is(err, ErrImageInUse):
			// Something outside Shipyard holds it; that must not keep the
			// app alive.
			r.event(ctx, store.LevelWarn, "image %s is still in use and stays on the host", id)
		case err != nil:
			return fmt.Errorf("remove image %s: %w", id, err)
		default:
			images++
		}
	}
	r.event(ctx, store.LevelInfo, "%d images removed", images)

	if err := r.phase(ctx, PhaseRemove); err != nil {
		return err
	}
	if err := r.Store.FinishAppDelete(ctx, r.app.ID, r.op.ID, r.owner, r.audit(store.AuditSuccess)); err != nil {
		return fmt.Errorf("remove app: %w", err)
	}
	r.log.Info("app deleted", slog.String("app", r.app.Slug), slog.Int("containers", removed), slog.Int("images", images))
	return nil
}

func (r *deleteRun) audit(result string) store.AuditEvent {
	return store.AuditEvent{Actor: "system", Action: AuditAppDelete, Target: "app:" + r.app.Slug + " operation:" + r.op.ID, Result: result}
}

// fail records a failed delete. The app stays, possibly out of service and
// partly removed; deleting it again finishes the job, since every step is
// idempotent.
func (r *deleteRun) fail(ctx context.Context, cause error) error {
	reason := cause.Error()
	r.log.Warn("delete failed", slog.String("reason", firstLine(reason)))
	r.event(ctx, store.LevelError, "delete failed: %s; delete the app again to finish", reason)
	if r.app.ID != "" {
		if _, err := r.Store.RecordAudit(ctx, r.audit(store.AuditFailure)); err != nil {
			r.log.Error("audit event not recorded", slog.Any("err", err))
		}
	}
	if _, err := r.Store.FailOperation(ctx, r.op.ID, r.owner, reason, 0); err != nil {
		return fmt.Errorf("record failure: %w", err)
	}
	return nil
}

func (r *deleteRun) phase(ctx context.Context, phase string) error {
	if err := r.Store.SetOperationPhase(ctx, r.op.ID, r.owner, phase); err != nil {
		return fmt.Errorf("phase %s: %w", phase, err)
	}
	return nil
}

func (r *deleteRun) event(ctx context.Context, level, format string, args ...any) {
	if _, err := r.Store.AppendOperationEvent(ctx, r.op.ID, level, fmt.Sprintf(format, args...)); err != nil && ctx.Err() == nil {
		r.log.Warn("append operation event failed", slog.Any("err", err))
	}
}
