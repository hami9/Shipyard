package store

import (
	"context"
	"encoding/json"
	"errors"
)

// CatchUpApp is an auto-deploy app whose branch head the catch-up reads.
type CatchUpApp struct {
	ID, Slug, Repo, Branch string
	InstallationID         int64 // 0: a public repository
}

// CatchUpKey is the idempotency key of the one catch-up deploy of a commit.
func CatchUpKey(appID, sha string) string { return "catchup:" + appID + ":" + sha }

// CatchUpCandidates returns the apps the catch-up checks (P4.5): auto_deploy
// on, deployed at least once (a new app waits for its first deploy), and no
// operation queued or running (that one is newer than any catch-up).
func (s *Store) CatchUpCandidates(ctx context.Context) ([]CatchUpApp, error) {
	rows, err := s.q.Query(ctx, `
		SELECT a.id, a.slug, a.repo_full_name, a.branch, coalesce(a.github_installation_id, 0) FROM apps a
		WHERE a.auto_deploy
		  AND EXISTS (SELECT 1 FROM deployments d WHERE d.app_id = a.id)
		  AND NOT EXISTS (SELECT 1 FROM operations o WHERE o.app_id = a.id AND o.status IN ('queued', 'running'))
		ORDER BY a.slug`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []CatchUpApp
	for rows.Next() {
		var a CatchUpApp
		if err := rows.Scan(&a.ID, &a.Slug, &a.Repo, &a.Branch, &a.InstallationID); err != nil {
			return nil, mapError(err)
		}
		out = append(out, a)
	}
	return out, mapError(rows.Err())
}

// EnqueueCatchUp queues a deploy of sha, which the caller read as the head
// of branch, unless one of these holds (then created is false):
//
//   - the app tried that commit before: a deployment of it in any status, so
//     a rollback is not undone and a failed commit is not retried, or an
//     earlier catch-up of it;
//   - the app changed meanwhile: auto_deploy off, another branch, or an
//     operation queued or running;
//   - the app is being deleted.
//
// The app row lock orders it with the webhook receiver's enqueue: whichever
// comes second sees the other's operation.
func (s *Store) EnqueueCatchUp(ctx context.Context, appID, branch, sha string) (op Operation, created bool, err error) {
	err = s.InTx(ctx, func(tx *Store) error {
		if err := tx.LockApp(ctx, appID); err != nil {
			return err
		}
		var skip bool
		if err := tx.q.QueryRow(ctx, `SELECT
				NOT a.auto_deploy OR a.branch <> $2
				OR EXISTS (SELECT 1 FROM deployments d WHERE d.app_id = a.id AND d.source_commit_sha = $3)
				OR EXISTS (SELECT 1 FROM operations o WHERE o.idempotency_key = $4)
				OR EXISTS (SELECT 1 FROM operations o WHERE o.app_id = a.id AND o.status IN ('queued', 'running'))
			FROM apps a WHERE a.id = $1`, appID, branch, sha, CatchUpKey(appID, sha)).Scan(&skip); err != nil {
			return mapError(err)
		}
		if skip {
			return nil
		}
		payload, _ := json.Marshal(struct {
			Ref string `json:"ref"`
		}{sha})
		res, err := tx.EnqueueOperation(ctx, NewOperation{AppID: appID, Kind: "deploy", IdempotencyKey: CatchUpKey(appID, sha), Payload: payload})
		if errors.Is(err, ErrAppDeleting) {
			return nil
		}
		if err != nil {
			return err
		}
		op, created = res.Operation, res.Created
		return nil
	})
	return op, created, err
}
