package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

type janitorFakes struct {
	deps       map[string]store.Deployment
	apps       map[string]store.App
	containers []ManagedContainer
	calls      []string
	depErr     error
	removeErr  map[string]error
}

func (f *janitorFakes) DeploymentByID(_ context.Context, id string) (store.Deployment, error) {
	if f.depErr != nil {
		return store.Deployment{}, f.depErr
	}
	d, ok := f.deps[id]
	if !ok {
		return store.Deployment{}, store.ErrNotFound
	}
	return d, nil
}
func (f *janitorFakes) AppByID(_ context.Context, id string) (store.App, error) {
	a, ok := f.apps[id]
	if !ok {
		return store.App{}, store.ErrNotFound
	}
	return a, nil
}
func (f *janitorFakes) ListManaged(context.Context) ([]ManagedContainer, error) {
	return f.containers, nil
}
func (f *janitorFakes) Stop(_ context.Context, id string, d time.Duration) error {
	f.calls = append(f.calls, "stop "+id+" "+d.String())
	return nil
}
func (f *janitorFakes) Remove(_ context.Context, id string) error {
	f.calls = append(f.calls, "remove "+id)
	return f.removeErr[id]
}

func TestJanitorSweep(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	f := &janitorFakes{
		apps: map[string]store.App{"app-1": {ID: "app-1", StopTimeout: 7 * time.Second}},
		deps: map[string]store.Deployment{
			"old":      {ID: "old", AppID: "app-1", Status: store.DeploySuperseded, EndedAt: ago(6 * time.Minute)},
			"recent":   {ID: "recent", AppID: "app-1", Status: store.DeploySuperseded, EndedAt: ago(time.Minute)},
			"edge":     {ID: "edge", AppID: "app-1", Status: store.DeploySuperseded, EndedAt: ago(5 * time.Minute)},
			"active":   {ID: "active", AppID: "app-1", Status: store.DeployActive},
			"checking": {ID: "checking", AppID: "app-1", Status: store.DeployHealthChecking},
			"failed":   {ID: "failed", AppID: "app-1", Status: store.DeployFailed},
			"orphan":   {ID: "orphan", AppID: "gone-app", Status: store.DeploySuperseded, EndedAt: ago(time.Hour)},
		},
		containers: []ManagedContainer{
			{ID: "c-old", DeploymentID: "old", Running: true},
			{ID: "c-recent", DeploymentID: "recent", Running: true},
			{ID: "c-edge", DeploymentID: "edge", Running: true}, // exactly at the window: drained
			{ID: "c-active", DeploymentID: "active", Running: true},
			{ID: "c-checking", DeploymentID: "checking", Running: true},
			{ID: "c-failed", DeploymentID: "failed", Running: true},
			{ID: "c-foreign", DeploymentID: "elsewhere", Running: true}, // another database's: kept
			{ID: "c-orphan", DeploymentID: "orphan", Running: true},
			{ID: "c-exited", DeploymentID: "old", Running: false},
		},
	}
	j := &Janitor{Store: f, Runtime: f, Window: 5 * time.Minute, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	removed, err := j.Sweep(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	// Superseded past the window: graceful stop with the app's stop_timeout,
	// then removal; failed and app-less: removed at once; an exited container
	// is not stopped again; one this database does not know is left alone.
	want := []string{"stop c-old 7s", "remove c-old", "stop c-edge 7s", "remove c-edge", "remove c-failed",
		"remove c-orphan", "remove c-exited"}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls =\n%v\nwant\n%v", f.calls, want)
	}
	if strings.Join(removed, ",") != "c-old,c-edge,c-failed,c-orphan,c-exited" {
		t.Fatalf("removed = %v", removed)
	}
}

// One failing container does not stop the sweep; the errors are reported.
func TestJanitorCarriesOn(t *testing.T) {
	f := &janitorFakes{
		deps: map[string]store.Deployment{"a": {Status: store.DeployFailed}, "b": {Status: store.DeployFailed}},
		containers: []ManagedContainer{
			{ID: "c-a", DeploymentID: "a"}, {ID: "c-b", DeploymentID: "b"},
		},
		removeErr: map[string]error{"c-a": errors.New("device busy")},
	}
	j := &Janitor{Store: f, Runtime: f, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	removed, err := j.Sweep(t.Context(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "device busy") || strings.Join(removed, ",") != "c-b" {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	// A database error removes nothing: without the truth, keep everything.
	f.depErr, f.calls = errors.New("db down"), nil
	if removed, err := j.Sweep(t.Context(), time.Now()); err == nil || len(removed) != 0 || len(f.calls) != 0 {
		t.Fatalf("db down: removed %v, calls %v, err %v", removed, f.calls, err)
	}
}
