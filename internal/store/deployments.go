package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Deployment statuses (ARCHITECTURE §5).
const (
	DeployBuilding       = "building"
	DeployStarting       = "starting"
	DeployHealthChecking = "health_checking"
	DeploySwitching      = "switching"
	DeployActive         = "active"
	DeploySuperseded     = "superseded"
	DeployFailed         = "failed"
)

// Deployment is one attempt to release a commit of an app.
type Deployment struct {
	ID               string
	AppID            string
	OperationID      string
	Kind             string
	SourceCommitSHA  string
	ImageID          string // empty until built
	BuildMetadata    json.RawMessage
	EnvRevisionID    *string // nil: the app had no environment
	ContainerID      string  // empty until created
	Status           string
	FailureReason    string
	BuildingAt       *time.Time
	StartingAt       *time.Time
	HealthCheckingAt *time.Time
	SwitchingAt      *time.Time
	ActiveAt         *time.Time
	EndedAt          *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// NewDeployment is the input to CreateDeployment.
type NewDeployment struct {
	OperationID     string
	SourceCommitSHA string
	EnvRevisionID   *string
}

const deployColumns = `id, app_id, operation_id, kind, source_commit_sha, coalesce(image_id, ''),
	build_metadata, env_revision_id, coalesce(container_id, ''), status, coalesce(failure_reason, ''),
	building_at, starting_at, health_checking_at, switching_at, active_at, ended_at, created_at, updated_at`

func scanDeployment(row interface{ Scan(...any) error }) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.AppID, &d.OperationID, &d.Kind, &d.SourceCommitSHA, &d.ImageID,
		&d.BuildMetadata, &d.EnvRevisionID, &d.ContainerID, &d.Status, &d.FailureReason,
		&d.BuildingAt, &d.StartingAt, &d.HealthCheckingAt, &d.SwitchingAt, &d.ActiveAt, &d.EndedAt, &d.CreatedAt, &d.UpdatedAt)
	return d, mapError(err)
}

// Every write below is guarded by the operation's lease, like the operation
// updates: a worker that lost its lease cannot change a deployment another
// worker now owns (invariant 3). Zero rows means ErrLeaseLost.

// CreateDeployment inserts the build deployment of a running operation, in
// status building. The API never creates deployments: a cancelled request
// has none (ARCHITECTURE §5 step 1).
func (s *Store) CreateDeployment(ctx context.Context, owner string, n NewDeployment) (Deployment, error) {
	d, err := scanDeployment(s.q.QueryRow(ctx, `
		INSERT INTO deployments (app_id, operation_id, kind, source_commit_sha, env_revision_id, status, building_at)
		SELECT o.app_id, o.id, 'build', $3, $4, 'building', now() FROM operations o
		WHERE o.id = $1 AND o.lease_owner = $2 AND o.status = 'running'
		RETURNING `+deployColumns, n.OperationID, owner, n.SourceCommitSHA, n.EnvRevisionID))
	if errors.Is(err, ErrNotFound) {
		return Deployment{}, ErrLeaseLost
	}
	return d, err
}

// DeploymentByOperation returns the deployment an operation created, so a
// retried operation resumes it instead of starting over.
func (s *Store) DeploymentByOperation(ctx context.Context, opID string) (Deployment, error) {
	return scanDeployment(s.q.QueryRow(ctx, `SELECT `+deployColumns+` FROM deployments WHERE operation_id = $1`, opID))
}

// DeploymentByID returns one deployment.
func (s *Store) DeploymentByID(ctx context.Context, id string) (Deployment, error) {
	return scanDeployment(s.q.QueryRow(ctx, `SELECT `+deployColumns+` FROM deployments WHERE id = $1`, id))
}

// ActiveDeployment returns the app's serving deployment, or ErrNotFound.
func (s *Store) ActiveDeployment(ctx context.Context, appID string) (Deployment, error) {
	return scanDeployment(s.q.QueryRow(ctx, `SELECT `+deployColumns+` FROM deployments WHERE app_id = $1 AND status = 'active'`, appID))
}

func (s *Store) ownedDeploymentUpdate(ctx context.Context, set, id, owner string, args ...any) error {
	tag, err := s.q.Exec(ctx, `UPDATE deployments d SET `+set+` FROM operations o
		WHERE d.id = $1 AND o.id = d.operation_id AND o.lease_owner = $2 AND o.status = 'running'
		  AND d.status IN ('building', 'starting', 'health_checking', 'switching')`, append([]any{id, owner}, args...)...)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// RecordImage stores the built image ID and moves to starting.
func (s *Store) RecordImage(ctx context.Context, id, owner, imageID string, metadata json.RawMessage) error {
	return s.ownedDeploymentUpdate(ctx, `image_id = $3, build_metadata = $4, status = 'starting',
		starting_at = coalesce(d.starting_at, now())`, id, owner, imageID, metadata)
}

// RecordContainer stores the container ID before the container starts
// (ARCHITECTURE §5 step 5).
func (s *Store) RecordContainer(ctx context.Context, id, owner, containerID string) error {
	return s.ownedDeploymentUpdate(ctx, `container_id = $3`, id, owner, containerID)
}

// MarkHealthChecking records that the candidate runs and is being probed.
func (s *Store) MarkHealthChecking(ctx context.Context, id, owner string) error {
	return s.ownedDeploymentUpdate(ctx, `status = 'health_checking',
		health_checking_at = coalesce(d.health_checking_at, now())`, id, owner)
}

// MarkSwitching records that the healthy candidate's routes are being loaded
// and verified (ARCHITECTURE §5 step 7).
func (s *Store) MarkSwitching(ctx context.Context, id, owner string) error {
	return s.ownedDeploymentUpdate(ctx, `status = 'switching', switching_at = coalesce(d.switching_at, now())`, id, owner)
}

// FailDeployment ends a deployment as failed. The reason must not contain
// secrets; it is shown to operators.
func (s *Store) FailDeployment(ctx context.Context, id, owner, reason string) error {
	if reason == "" {
		reason = "unknown error"
	}
	return s.ownedDeploymentUpdate(ctx, `status = 'failed', failure_reason = $3, ended_at = now()`, id, owner, reason)
}

// ActivateDeployment makes a healthy deployment the app's active one, points
// the app's verified hostnames at upstream, ends the previous active
// deployment as superseded, and completes the operation, in one transaction
// (ARCHITECTURE §5 step 7). The verified hostnames must all still exist.
// Routes added meanwhile, which point at the previous deployment or at none,
// follow; routes on an older deployment stay. It returns the superseded
// deployment, if any, so the caller can drain its container.
func (s *Store) ActivateDeployment(ctx context.Context, id, owner, upstream string, hostnames []string) (*Deployment, error) {
	var prev *Deployment
	err := s.InTx(ctx, func(tx *Store) error {
		d, err := tx.DeploymentByID(ctx, id)
		if err != nil {
			return err
		}
		if err := tx.LockApp(ctx, d.AppID); err != nil {
			return err
		}
		old, err := scanDeployment(tx.q.QueryRow(ctx, `
			UPDATE deployments SET status = 'superseded', ended_at = now()
			WHERE app_id = $1 AND status = 'active' AND id <> $2
			RETURNING `+deployColumns, d.AppID, id))
		switch {
		case err == nil:
			prev = &old
		case !errors.Is(err, ErrNotFound):
			return err
		}
		if err := tx.ownedDeploymentUpdate(ctx, `status = 'active', active_at = now()`, id, owner); err != nil {
			return err
		}
		if len(hostnames) > 0 {
			tag, err := tx.q.Exec(ctx, `UPDATE routes SET deployment_id = $2, upstream = $3
				WHERE app_id = $1 AND hostname = ANY($4::text[])`, d.AppID, id, upstream, hostnames)
			if err != nil {
				return mapError(err)
			}
			// A verified hostname that vanished (deleted meanwhile) must not
			// be committed as served.
			if tag.RowsAffected() != int64(len(hostnames)) {
				return fmt.Errorf("%w: routes changed during the switch", ErrConflict)
			}
		}
		// A route added during the switch was attached to the then-active
		// deployment, or to none on a first deploy (CreateRoute). It follows
		// the app to the new one rather than stay on a container about to be
		// drained or on "no active deployment". The worker's sync then loads it.
		if upstream != "" {
			var prevID *string
			if prev != nil {
				prevID = &prev.ID
			}
			if _, err := tx.q.Exec(ctx, `UPDATE routes SET deployment_id = $2, upstream = $3
				WHERE app_id = $1 AND (deployment_id IS NULL OR deployment_id = $4)`, d.AppID, id, upstream, prevID); err != nil {
				return mapError(err)
			}
		}
		return tx.CompleteOperation(ctx, d.OperationID, owner)
	})
	if err != nil {
		return nil, err
	}
	return prev, nil
}
