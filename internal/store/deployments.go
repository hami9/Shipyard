package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	DeployCancelled      = "cancelled"
)

// Deployment is one attempt to release a commit of an app.
type Deployment struct {
	ID               string
	AppID            string
	OperationID      string
	Kind             string
	SourceDeployment *string // a rollback's source; nil for a build
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
	// GitHubDeploymentID is the GitHub Deployment reporting it (P4.6); 0: none.
	GitHubDeploymentID int64
}

// NewDeployment is the input to CreateDeployment.
type NewDeployment struct {
	OperationID     string
	SourceCommitSHA string
	EnvRevisionID   *string
}

const deployColumns = `id, app_id, operation_id, kind, source_deployment_id, source_commit_sha, coalesce(image_id, ''),
	build_metadata, env_revision_id, coalesce(container_id, ''), status, coalesce(failure_reason, ''),
	building_at, starting_at, health_checking_at, switching_at, active_at, ended_at, created_at, updated_at,
	coalesce(github_deployment_id, 0)`

// scanDeployment reads deployColumns, then any extra columns into extra.
func scanDeployment(row interface{ Scan(...any) error }, extra ...any) (Deployment, error) {
	var d Deployment
	err := row.Scan(append([]any{&d.ID, &d.AppID, &d.OperationID, &d.Kind, &d.SourceDeployment, &d.SourceCommitSHA, &d.ImageID,
		&d.BuildMetadata, &d.EnvRevisionID, &d.ContainerID, &d.Status, &d.FailureReason,
		&d.BuildingAt, &d.StartingAt, &d.HealthCheckingAt, &d.SwitchingAt, &d.ActiveAt, &d.EndedAt, &d.CreatedAt, &d.UpdatedAt,
		&d.GitHubDeploymentID},
		extra...)...)
	return d, mapError(err)
}

// Release is a deployment as the history shows it: with its environment
// revision's number (0: the app had no environment).
type Release struct {
	Deployment
	EnvRevision int
}

// Releases returns up to limit of the app's deployments, newest first. With
// before (a deployment ID of this app), it continues after that one, so a
// page stays stable while new deployments arrive. A before that is not the
// app's deployment is ErrNotFound.
func (s *Store) Releases(ctx context.Context, appID, before string, limit int) ([]Release, error) {
	var cursor any // nil: from the newest
	if before != "" {
		d, err := s.DeploymentByID(ctx, before)
		if err != nil {
			return nil, err
		}
		if d.AppID != appID {
			return nil, ErrNotFound
		}
		cursor = d.ID
	}
	// The scalar subquery keeps deployColumns unambiguous (no join).
	rows, err := s.q.Query(ctx, `
		SELECT `+deployColumns+`, coalesce((SELECT number FROM env_revisions r WHERE r.id = d.env_revision_id), 0)
		FROM deployments d
		WHERE app_id = $1 AND ($2::uuid IS NULL OR
			(created_at, id) < (SELECT created_at, id FROM deployments WHERE id = $2::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $3`, appID, cursor, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		var r Release
		if r.Deployment, err = scanDeployment(rows, &r.EnvRevision); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, mapError(rows.Err())
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

// NewRollback is the input to CreateRollbackDeployment.
type NewRollback struct {
	OperationID string
	Source      Deployment // the earlier deployment whose image it reuses
	// EnvRevisionID is the source's revision, or the latest one when the
	// operator asked for the current configuration; nil for none.
	EnvRevisionID *string
}

// CreateRollbackDeployment inserts a rollback operation's deployment: kind
// rollback, the source's commit and image ID (never rebuilt, invariant 6),
// straight in status starting. The source must be a deployment of the same
// app with an image.
func (s *Store) CreateRollbackDeployment(ctx context.Context, owner string, n NewRollback) (Deployment, error) {
	d, err := scanDeployment(s.q.QueryRow(ctx, `
		INSERT INTO deployments (app_id, operation_id, kind, source_deployment_id, source_commit_sha, image_id,
			build_metadata, env_revision_id, status, starting_at)
		SELECT o.app_id, o.id, 'rollback', src.id, src.source_commit_sha, src.image_id, src.build_metadata, $4, 'starting', now()
		FROM operations o JOIN deployments src ON src.id = $3 AND src.app_id = o.app_id AND src.image_id IS NOT NULL
		WHERE o.id = $1 AND o.lease_owner = $2 AND o.status = 'running'
		RETURNING `+deployColumns, n.OperationID, owner, n.Source.ID, n.EnvRevisionID))
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

// ActiveDeployments returns every app's serving deployment, for the
// reconciler (P3.2).
func (s *Store) ActiveDeployments(ctx context.Context) ([]Deployment, error) {
	rows, err := s.q.Query(ctx, `SELECT `+deployColumns+` FROM deployments WHERE status = 'active' ORDER BY app_id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, mapError(rows.Err())
}

// ReplaceContainer records the container the reconciler recreated for an
// active deployment whose container was gone. It changes nothing unless the
// deployment is still active with container old: a deploy that superseded
// it meanwhile wins, with ErrConflict.
func (s *Store) ReplaceContainer(ctx context.Context, id, old, container string) error {
	tag, err := s.q.Exec(ctx, `UPDATE deployments SET container_id = $3
		WHERE id = $1 AND status = 'active' AND container_id = $2`, id, old, container)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: deployment %s is no longer active with container %.12s", ErrConflict, id, old)
	}
	return nil
}

// PrunableImages returns which of the present image IDs retention may
// remove (ADR-0006): those a deployment in this database recorded, except
//   - the images of each app's last keep releases that served traffic
//     (superseded; an image served several times counts once),
//   - the active release's image,
//   - images of deployments still in progress,
//   - the target image of a queued or running rollback.
//
// An image no deployment here recorded is never returned: it belongs to
// another installation, or a build whose ID is not persisted yet.
func (s *Store) PrunableImages(ctx context.Context, present []string, keep int) ([]string, error) {
	rows, err := s.q.Query(ctx, `
		WITH served AS (
			SELECT app_id, image_id, max(coalesce(active_at, created_at)) AS last_served
			FROM deployments
			WHERE status = 'superseded' AND image_id IS NOT NULL
			GROUP BY app_id, image_id
		), ranked AS (
			SELECT image_id, row_number() OVER (PARTITION BY app_id ORDER BY last_served DESC, image_id) AS n
			FROM served
			WHERE image_id NOT IN (SELECT image_id FROM deployments WHERE status = 'active')
		), kept AS (
			SELECT image_id FROM ranked WHERE n <= $2
			UNION
			SELECT image_id FROM deployments
			WHERE image_id IS NOT NULL AND status NOT IN ('superseded', 'failed', 'cancelled')
			UNION
			SELECT d.image_id FROM operations o
			JOIN deployments d ON d.app_id = o.app_id AND d.id::text = o.payload->>'target'
			WHERE o.kind = 'rollback' AND o.status IN ('queued', 'running') AND d.image_id IS NOT NULL
		)
		SELECT DISTINCT image_id FROM deployments
		WHERE image_id = ANY($1) AND image_id NOT IN (SELECT image_id FROM kept)
		ORDER BY image_id`, present, keep)
	if err != nil {
		return nil, mapError(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return ids, mapError(err)
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

// RecordGitHubDeployment stores the GitHub Deployment that reports this one
// (P4.6). The first one stays: a resumed run that created another keeps
// reporting to the recorded one.
func (s *Store) RecordGitHubDeployment(ctx context.Context, id, owner string, githubID int64) error {
	return s.ownedDeploymentUpdate(ctx, `github_deployment_id = coalesce(d.github_deployment_id, $3)`, id, owner, githubID)
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
// deployment, if any; the reconciler drains its container after the
// observation window (ended_at is its start).
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
