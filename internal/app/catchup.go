package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hami9/shipyard/internal/store"
)

// CatchUpStore is what the catch-up needs from persistence.
type CatchUpStore interface {
	CatchUpCandidates(ctx context.Context) ([]store.CatchUpApp, error)
	EnqueueCatchUp(ctx context.Context, appID, branch, sha string) (store.Operation, bool, error)
}

// BranchHeads reads a branch's tip from the repository, through the
// app's GitHub App installation when it has one (internal/source).
type BranchHeads interface {
	Head(ctx context.Context, repo, branch string, installationID int64) (string, error)
}

// CatchUp deploys pushes whose webhook never arrived (ROADMAP P4.5): GitHub
// does not redeliver a failed delivery by itself [GH-REDELIVER], so a push
// made while Shipyard was down would otherwise wait for the next one. For
// each idle auto-deploy app it reads the tracked branch's head and, when the
// app has never tried that commit, queues a deploy of it
// (store.EnqueueCatchUp has the rules).
type CatchUp struct {
	Store CatchUpStore
	Heads BranchHeads
	Log   *slog.Logger
}

// Run checks every candidate once and returns the operations it queued. One
// app's failure (GitHub unreachable, a deleted branch) does not stop the
// others; the errors come back joined.
func (c *CatchUp) Run(ctx context.Context) ([]string, error) {
	apps, err := c.Store.CatchUpCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list apps to catch up: %w", err)
	}
	var (
		queued []string
		errs   []error
	)
	for _, a := range apps {
		head, err := c.Heads.Head(ctx, a.Repo, a.Branch, a.InstallationID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: read the head of %s: %w", a.Slug, a.Branch, err))
			continue
		}
		op, created, err := c.Store.EnqueueCatchUp(ctx, a.ID, a.Branch, head)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: queue %s: %w", a.Slug, head, err))
			continue
		}
		if created {
			c.Log.InfoContext(ctx, "missed push: deploying the branch head", slog.String("app", a.Slug),
				slog.String("branch", a.Branch), slog.String("commit", head), slog.String("operation_id", op.ID))
			queued = append(queued, op.ID)
		}
	}
	return queued, errors.Join(errs...)
}
