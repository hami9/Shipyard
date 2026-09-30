package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

type imageFakes struct {
	present   []string
	prunable  []string
	keep      int
	gotIDs    []string
	removed   []string
	removeErr map[string]error
	listErr   error
}

func (f *imageFakes) ListImages(context.Context) ([]string, error) { return f.present, f.listErr }
func (f *imageFakes) PrunableImages(_ context.Context, present []string, keep int) ([]string, error) {
	f.gotIDs, f.keep = present, keep
	return f.prunable, nil
}
func (f *imageFakes) RemoveImage(_ context.Context, id string) error {
	if err := f.removeErr[id]; err != nil {
		return err
	}
	f.removed = append(f.removed, id)
	return nil
}

// P3.4: the database picks from the images present; an image in use stays
// quietly for a later pass; another failure is reported but does not stop
// the pass.
func TestImagePrune(t *testing.T) {
	f := &imageFakes{
		present:  []string{"sha256:a", "sha256:b", "sha256:c", "sha256:d"},
		prunable: []string{"sha256:b", "sha256:c", "sha256:d"},
		removeErr: map[string]error{
			"sha256:c": errors.Join(ErrImageInUse, errors.New("conflict")),
			"sha256:d": errors.New("daemon down"),
		},
	}
	p := &ImagePruner{Store: f, Runtime: f, Keep: 5, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	removed, err := p.Prune(t.Context())
	if !slices.Equal(removed, []string{"sha256:b"}) || !slices.Equal(f.removed, removed) {
		t.Fatalf("removed %v (runtime %v), want [sha256:b]", removed, f.removed)
	}
	if err == nil || !strings.Contains(err.Error(), "sha256:d") || strings.Contains(err.Error(), "sha256:c") {
		t.Fatalf("err = %v, want only sha256:d's failure", err)
	}
	if f.keep != 5 || !slices.Equal(f.gotIDs, f.present) {
		t.Fatalf("store asked with keep=%d present=%v", f.keep, f.gotIDs)
	}
}

// No image of ours on the host: nothing to ask; a failed listing is returned.
func TestImagePruneNothing(t *testing.T) {
	f := &imageFakes{prunable: []string{"sha256:x"}}
	p := &ImagePruner{Store: f, Runtime: f, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if removed, err := p.Prune(t.Context()); err != nil || removed != nil || f.gotIDs != nil {
		t.Fatalf("removed %v, err %v, store asked %v", removed, err, f.gotIDs)
	}
	f.listErr = errors.New("daemon down")
	if _, err := p.Prune(t.Context()); err == nil {
		t.Fatal("a failed listing was not reported")
	}
}
