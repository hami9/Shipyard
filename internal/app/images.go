package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ErrImageInUse: Docker keeps the image: a container still uses it, or it
// has a second tag.
var ErrImageInUse = errors.New("image in use")

// ImageStore picks what retention may remove (store.PrunableImages).
type ImageStore interface {
	PrunableImages(ctx context.Context, present []string, keep int) ([]string, error)
}

// ImageRuntime lists and removes the images Shipyard built
// (internal/runtime). RemoveImage reports ErrImageInUse for an image a
// container uses.
type ImageRuntime interface {
	ListImages(ctx context.Context) ([]string, error)
	RemoveImage(ctx context.Context, id string) error
}

// ImagePruner is image retention (ADR-0006): each app keeps the images of
// its active release and its last Keep releases that served traffic, the
// rollback targets (invariant 6). The images of failed deploys and of older
// releases go. The database decides and Docker follows (invariant 2), so
// an image no deployment in this database recorded is never removed.
type ImagePruner struct {
	Store   ImageStore
	Runtime ImageRuntime
	Keep    int
	Log     *slog.Logger
}

// Prune runs one pass and reports what it removed. An image in use stays
// until a later pass; other failures are returned joined.
func (p *ImagePruner) Prune(ctx context.Context) (removed []string, err error) {
	present, err := p.Runtime.ListImages(ctx)
	if err != nil || len(present) == 0 {
		return nil, err
	}
	ids, err := p.Store.PrunableImages(ctx, present, p.Keep)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, id := range ids {
		err := p.Runtime.RemoveImage(ctx, id)
		switch {
		case errors.Is(err, ErrImageInUse):
			p.Log.Debug("image in use; kept for now", slog.String("image_id", id))
		case err != nil:
			errs = append(errs, fmt.Errorf("image %s: %w", id, err))
		default:
			p.Log.Info("image removed by retention", slog.String("image_id", id))
			removed = append(removed, id)
		}
	}
	return removed, errors.Join(errs...)
}
