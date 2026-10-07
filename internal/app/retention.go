package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hami9/shipyard/internal/store"
)

// EventStore trims stored operation events (store.TrimOperationEvents).
type EventStore interface {
	TrimOperationEvents(ctx context.Context, keep int, maxBytes int64) (store.Trimmed, error)
}

// BuildCache prunes the builder's cache down to a cap (build.PruneCache).
type BuildCache interface {
	PruneCache(ctx context.Context, maxUsed int64) (reclaimed string, err error)
}

// Retention is the periodic job of ADR-0006 besides images (ImagePruner,
// run by the reconciler): the BuildKit cache cap and the operation event
// cap. Both steps run even if one fails.
type Retention struct {
	Events EventStore
	Cache  BuildCache
	// CacheMax is the most build cache kept, in bytes.
	CacheMax int64
	// KeepOperations is how many of each app's newest operations keep
	// their events; MaxEventBytes caps one finished operation's events.
	KeepOperations int
	MaxEventBytes  int64
	Log            *slog.Logger
}

// Run does one pass.
func (r *Retention) Run(ctx context.Context) error {
	var errs []error
	if reclaimed, err := r.Cache.PruneCache(ctx, r.CacheMax); err != nil {
		errs = append(errs, err)
	} else {
		r.Log.Info("build cache pruned", slog.String("reclaimed", reclaimed), slog.Int64("max_bytes", r.CacheMax))
	}
	t, err := r.Events.TrimOperationEvents(ctx, r.KeepOperations, r.MaxEventBytes)
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("trim operation events: %w", err))
	case t.Events > 0:
		r.Log.Info("operation events trimmed", slog.Int("operations", t.Operations), slog.Int64("events", t.Events))
	}
	return errors.Join(errs...)
}
