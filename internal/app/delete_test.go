package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// deleteFakes records every call in order, across all ports.
type deleteFakes struct {
	calls      []string
	containers []ManagedContainer
	images     []string // present on the host
	art        store.Artifacts
	failAt     string // a call prefix that fails
	inUse      string // an image Docker keeps
	leaseLost  bool
	opStatus   string
	opReason   string
	audits     []store.AuditEvent
	events     []string
}

func (f *deleteFakes) call(format string, a ...any) error {
	c := fmt.Sprintf(format, a...)
	f.calls = append(f.calls, c)
	if f.failAt != "" && strings.HasPrefix(c, f.failAt) {
		return errors.New("boom")
	}
	return nil
}

func (f *deleteFakes) AppByID(context.Context, string) (store.App, error) {
	return store.App{ID: "app-1", Slug: "web", StopTimeout: 7 * time.Second}, nil
}
func (f *deleteFakes) SetOperationPhase(_ context.Context, _, _, phase string) error {
	if f.leaseLost {
		return store.ErrLeaseLost
	}
	return f.call("phase %s", phase)
}
func (f *deleteFakes) FailOperation(_ context.Context, _, _, reason string, _ time.Duration) (string, error) {
	f.opStatus, f.opReason = store.OpFailed, reason
	return store.OpFailed, nil
}
func (f *deleteFakes) AppendOperationEvent(_ context.Context, _, level, msg string) (store.OperationEvent, error) {
	f.events = append(f.events, level+": "+msg)
	return store.OperationEvent{}, nil
}
func (f *deleteFakes) StopServing(context.Context, string, string, string) ([]string, error) {
	return []string{"web.example.com"}, f.call("stop-serving")
}
func (f *deleteFakes) AppArtifacts(context.Context, string) (store.Artifacts, error) {
	return f.art, nil
}
func (f *deleteFakes) FinishAppDelete(_ context.Context, _, _, _ string, a store.AuditEvent) error {
	if err := f.call("finish"); err != nil {
		return err
	}
	f.audits, f.opStatus = append(f.audits, a), "gone"
	return nil
}
func (f *deleteFakes) RecordAudit(_ context.Context, a store.AuditEvent) (store.AuditEvent, error) {
	f.audits = append(f.audits, a)
	return a, nil
}
func (f *deleteFakes) ListManaged(context.Context) ([]ManagedContainer, error) {
	return f.containers, nil
}
func (f *deleteFakes) Stop(_ context.Context, id string, d time.Duration) error {
	return f.call("stop %s %s", id, d)
}
func (f *deleteFakes) Remove(_ context.Context, id string) error { return f.call("remove %s", id) }
func (f *deleteFakes) RemoveNetwork(_ context.Context, app string) error {
	return f.call("network %s", app)
}
func (f *deleteFakes) ListImages(context.Context) ([]string, error) { return f.images, nil }
func (f *deleteFakes) RemoveImage(_ context.Context, id string) error {
	if id == f.inUse {
		return fmt.Errorf("%w: %s", ErrImageInUse, id)
	}
	return f.call("image %s", id)
}
func (f *deleteFakes) Sync(context.Context) (bool, error) { return true, f.call("sync") }

func newDeleteFakes() (*deleteFakes, *Deleter) {
	f := &deleteFakes{
		art: store.Artifacts{Deployments: []string{"d1", "d2"}, Images: []string{"sha256:1", "sha256:2", "sha256:gone"}},
		containers: []ManagedContainer{
			{ID: "c-old", App: "web", DeploymentID: "d1"},                 // stopped
			{ID: "c-live", App: "web", DeploymentID: "d2", Running: true}, // serving
			{ID: "c-other", App: "api", DeploymentID: "d9", Running: true},
			{ID: "c-foreign", App: "web", DeploymentID: "dX", Running: true}, // another database's "web"
		},
		images: []string{"sha256:1", "sha256:2", "sha256:other"},
	}
	return f, &Deleter{Store: f, Runtime: f, Router: f, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

var deleteOp = store.Operation{ID: opID, AppID: "app-1", Kind: KindDelete, Attempt: 1}

// P3.8: a delete takes the app out of service first (database, then Caddy),
// then removes its containers (a running one stopped with the app's stop
// timeout), its network, and its recorded images, and last the app row with
// an audit event. It touches nothing of another app or another database.
func TestDelete(t *testing.T) {
	f, d := newDeleteFakes()
	if err := d.Run(t.Context(), deleteOp, owner); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"phase release", "stop-serving", "sync",
		"phase containers", "remove c-old", "stop c-live 7s", "remove c-live",
		"phase network", "network web",
		"phase images", "image sha256:1", "image sha256:2",
		"phase remove", "finish",
	}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls =\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if len(f.audits) != 1 || f.audits[0] != (store.AuditEvent{Actor: "system", Action: AuditAppDelete, Target: "app:web operation:" + opID, Result: store.AuditSuccess}) {
		t.Fatalf("audit = %+v", f.audits)
	}
	if f.opStatus != "gone" || !slices.Contains(f.events, "info: out of service: web.example.com") {
		t.Fatalf("op %s, events %v", f.opStatus, f.events)
	}

	// Without Caddy there is nothing to sync; an image something else holds
	// stays, with a warning, and does not stop the delete.
	f, d = newDeleteFakes()
	d.Router, f.inUse = nil, "sha256:1"
	if err := d.Run(t.Context(), deleteOp, owner); err != nil || f.opStatus != "gone" {
		t.Fatalf("run: %v, op %s %q", err, f.opStatus, f.opReason)
	}
	if slices.Contains(f.calls, "sync") || slices.Contains(f.calls, "image sha256:1") || !slices.Contains(f.calls, "image sha256:2") ||
		!slices.Contains(f.events, "warn: image sha256:1 is still in use and stays on the host") {
		t.Fatalf("calls %v\nevents %v", f.calls, f.events)
	}
}

// A step that fails ends the operation as failed, with a failure audit
// event, and nothing after it runs: the app row stays, so the delete can be
// repeated. Caddy failing to drop the routes stops before any container.
func TestDeleteFails(t *testing.T) {
	for failAt, tc := range map[string]struct {
		reason string
		last   string // the last call made
	}{
		"stop-serving":   {"take out of service: boom", "stop-serving"},
		"sync":           {"remove routes from caddy: boom", "sync"},
		"stop c-live":    {"stop container c-live: boom", "stop c-live 7s"},
		"remove c-old":   {"remove container c-old: boom", "remove c-old"},
		"network":        {"remove network: boom", "network web"},
		"image sha256:2": {"remove image sha256:2: boom", "image sha256:2"},
		"finish":         {"remove app: boom", "finish"},
	} {
		t.Run(failAt, func(t *testing.T) {
			f, d := newDeleteFakes()
			f.failAt = failAt
			if err := d.Run(t.Context(), deleteOp, owner); err != nil {
				t.Fatal(err)
			}
			if f.opStatus != store.OpFailed || f.opReason != tc.reason || f.calls[len(f.calls)-1] != tc.last {
				t.Fatalf("op %s %q, last call %q; want failed %q after %q", f.opStatus, f.opReason, f.calls[len(f.calls)-1], tc.reason, tc.last)
			}
			if len(f.audits) != 1 || f.audits[0].Result != store.AuditFailure || f.audits[0].Action != AuditAppDelete {
				t.Fatalf("audit = %+v", f.audits)
			}
			if !slices.ContainsFunc(f.events, func(e string) bool { return strings.Contains(e, "delete the app again to finish") }) {
				t.Fatalf("events = %v", f.events)
			}
		})
	}
}

// A lost lease records nothing: the next owner resumes, and says so.
func TestDeleteLeaseLostAndResume(t *testing.T) {
	f, d := newDeleteFakes()
	f.leaseLost = true
	if err := d.Run(t.Context(), deleteOp, owner); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("err = %v", err)
	}
	if f.opStatus != "" || len(f.audits) != 0 || len(f.calls) != 0 {
		t.Fatalf("op %q, audits %v, calls %v", f.opStatus, f.audits, f.calls)
	}

	f, d = newDeleteFakes()
	f.containers = nil // the first attempt removed them already
	retry := deleteOp
	retry.Attempt = 2
	if err := d.Run(t.Context(), retry, owner); err != nil || f.opStatus != "gone" {
		t.Fatalf("resume: %v, op %s", err, f.opStatus)
	}
	if !slices.Contains(f.events, "info: resuming the delete of web (attempt 2)") || !slices.Contains(f.events, "info: 0 containers removed") {
		t.Fatalf("events = %v", f.events)
	}
}
