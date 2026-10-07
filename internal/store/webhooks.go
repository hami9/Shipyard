package store

import (
	"context"
	"encoding/json"
	"errors"
)

// Outcomes of a recorded delivery (webhook_deliveries.outcome).
const (
	DeliveryQueued  = "queued"
	DeliveryIgnored = "ignored"
)

// AuditGitHubPush is the audit action of a recorded push delivery.
const AuditGitHubPush = "github.push"

// PushDelivery is a verified push to a branch, with its delivery id.
type PushDelivery struct {
	ID           string // X-GitHub-Delivery
	RepositoryID int64
	Repository   string // owner/name, matched against apps.repo_full_name
	Ref, Branch  string
	After        string // the commit to deploy
	RequestID    string // for the audit event
}

// PushRecord reports what RecordPush did.
type PushRecord struct {
	Duplicate  bool     // the delivery was recorded before; nothing new was done
	Outcome    string   // DeliveryQueued or DeliveryIgnored
	Reason     string   // why nothing was queued; empty for a duplicate
	Operations []string // the deploys queued for it, one per app
}

// WebhookKey is the idempotency key of the deploy that a delivery queues
// for an app. A push can deploy several apps (one repository and branch,
// different Dockerfiles), so the key names the app as well; the slug never
// changes.
func WebhookKey(delivery, slug string) string { return "gh:" + delivery + ":" + slug }

// RecordPush records a push delivery once and queues a deploy of its commit
// for every app that tracks the repository and branch with auto_deploy on
// (ROADMAP P4.2, P4.3).
//
//   - Deduplicated on the delivery id [GH-BP]: a redelivery, or the same
//     delivery arriving twice at once, finds the first record and returns
//     it with Duplicate set. The delivery row, the deploys, and the audit
//     event commit together, so a failure leaves no trace and GitHub's
//     redelivery starts over.
//   - Apps are matched by repository name, case-insensitively (the owner's
//     choice, 2026-10-04). repo_full_name is fixed at create, so a renamed
//     repository stops matching; matching by GitHub's repository id is for
//     when the GitHub App can look it up (P4.4).
//   - Each deploy goes through EnqueueOperation, so a newer push replaces
//     an app's still-queued deploy, and an app being deleted is skipped.
//   - The worker still checks that the commit is on the tracked branch
//     (invariant 7); the payload is trusted only as far as its signature.
func (s *Store) RecordPush(ctx context.Context, d PushDelivery) (PushRecord, error) {
	var rec PushRecord
	err := s.InTx(ctx, func(tx *Store) error {
		var id string
		err := mapError(tx.q.QueryRow(ctx, `
			INSERT INTO webhook_deliveries (delivery_id, event, repository_id, ref, after_sha)
			VALUES ($1, 'push', $2, $3, $4)
			ON CONFLICT (delivery_id) DO NOTHING RETURNING delivery_id`,
			d.ID, d.RepositoryID, d.Ref, d.After).Scan(&id))
		if errors.Is(err, ErrNotFound) {
			return tx.recordedPush(ctx, d.ID, &rec)
		}
		if err != nil {
			return err
		}
		deploy, reason, err := tx.appsToDeploy(ctx, d)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(struct {
			Ref string `json:"ref"`
		}{d.After})
		for _, a := range deploy {
			res, err := tx.EnqueueOperation(ctx, NewOperation{AppID: a.id, Kind: "deploy",
				IdempotencyKey: WebhookKey(d.ID, a.slug), Payload: payload})
			if errors.Is(err, ErrAppDeleting) {
				continue
			}
			if err != nil {
				return err
			}
			rec.Operations = append(rec.Operations, res.Operation.ID)
		}
		rec.Outcome, rec.Reason = DeliveryQueued, ""
		switch {
		case len(deploy) == 0:
			rec.Outcome, rec.Reason = DeliveryIgnored, reason
		case len(rec.Operations) == 0:
			rec.Outcome, rec.Reason = DeliveryIgnored, "every app tracking this branch is being deleted"
		}
		// operation_id holds one operation; with several, their keys find them.
		var single *string
		if len(rec.Operations) == 1 {
			single = &rec.Operations[0]
		}
		if _, err := tx.q.Exec(ctx, `UPDATE webhook_deliveries SET outcome = $2, operation_id = $3 WHERE delivery_id = $1`,
			d.ID, rec.Outcome, single); err != nil {
			return mapError(err)
		}
		_, err = tx.RecordAudit(ctx, AuditEvent{Actor: "webhook", Action: AuditGitHubPush, Target: "delivery:" + d.ID,
			Result: AuditSuccess, RequestID: d.RequestID})
		return err
	})
	if err != nil {
		return PushRecord{}, err
	}
	return rec, nil
}

type trackingApp struct{ id, slug string }

// appsToDeploy returns the apps a push deploys, in id order (the order
// their rows are locked in), or why there are none.
func (s *Store) appsToDeploy(ctx context.Context, d PushDelivery) ([]trackingApp, string, error) {
	rows, err := s.q.Query(ctx, `SELECT id, slug, branch, auto_deploy FROM apps
		WHERE lower(repo_full_name) = lower($1) ORDER BY id`, d.Repository)
	if err != nil {
		return nil, "", mapError(err)
	}
	defer rows.Close()
	var (
		deploy           []trackingApp
		known, onBranch  bool
		slug, branch, id string
		auto             bool
	)
	for rows.Next() {
		if err := rows.Scan(&id, &slug, &branch, &auto); err != nil {
			return nil, "", mapError(err)
		}
		known = true
		if branch != d.Branch {
			continue
		}
		onBranch = true
		if auto {
			deploy = append(deploy, trackingApp{id, slug})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", mapError(err)
	}
	switch {
	case !known:
		return nil, "no app deploys this repository", nil
	case !onBranch:
		return nil, "no app tracks branch " + d.Branch, nil
	case len(deploy) == 0:
		return nil, "auto-deploy is off for every app tracking this branch", nil
	}
	return deploy, "", nil
}

// recordedPush fills rec from a delivery recorded earlier.
func (s *Store) recordedPush(ctx context.Context, id string, rec *PushRecord) error {
	rec.Duplicate = true
	if err := s.q.QueryRow(ctx, `SELECT coalesce(outcome, '') FROM webhook_deliveries WHERE delivery_id = $1`,
		id).Scan(&rec.Outcome); err != nil {
		return mapError(err)
	}
	// Delivery ids are [A-Za-z0-9-] only, so the pattern has no wildcard but %.
	rows, err := s.q.Query(ctx, `SELECT id FROM operations WHERE idempotency_key LIKE $1 ORDER BY created_at, id`,
		WebhookKey(id, "%"))
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			return mapError(err)
		}
		rec.Operations = append(rec.Operations, op)
	}
	return mapError(rows.Err())
}
