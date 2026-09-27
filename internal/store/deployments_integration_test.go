//go:build integration

package store_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

var (
	testSHA       = strings.Repeat("ab", 20)
	testImage     = "sha256:" + strings.Repeat("0f", 32)
	testContainer = strings.Repeat("c1", 32)
)

// claimed enqueues and claims a deploy operation of app for owner.
func (f *queueFixture) claimed(app, owner string) store.Operation {
	f.t.Helper()
	f.enqueue(app, "k-"+uniq())
	op, err := f.s.ClaimOperation(f.t.Context(), owner, time.Minute)
	if err != nil || op.AppID != app {
		f.t.Fatalf("claim: %+v, %v", op, err)
	}
	return op
}

// healthy walks a new deployment of op to health_checking.
func (f *queueFixture) healthy(op store.Operation, owner string) store.Deployment {
	f.t.Helper()
	ctx := f.t.Context()
	d, err := f.s.CreateDeployment(ctx, owner, store.NewDeployment{OperationID: op.ID, SourceCommitSHA: testSHA})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, step := range []error{
		f.s.RecordImage(ctx, d.ID, owner, testImage, json.RawMessage(`{"containerimage.digest":"x"}`)),
		f.s.RecordContainer(ctx, d.ID, owner, testContainer),
		f.s.MarkHealthChecking(ctx, d.ID, owner),
	} {
		if step != nil {
			f.t.Fatal(step)
		}
	}
	return d
}

func TestDeploymentLifecycle(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	app := f.app()

	op := f.claimed(app, "w1")
	d := f.healthy(op, "w1")
	if d.Status != store.DeployBuilding || d.BuildingAt == nil || d.Kind != "build" || d.AppID != app || d.EnvRevisionID != nil {
		t.Fatalf("created = %+v", d)
	}
	got, err := f.s.DeploymentByOperation(ctx, op.ID)
	if err != nil || got.ID != d.ID || got.Status != store.DeployHealthChecking || got.ImageID != testImage ||
		got.ContainerID != testContainer || got.StartingAt == nil || got.HealthCheckingAt == nil {
		t.Fatalf("by operation = %+v, %v", got, err)
	}
	if _, err := f.s.ActiveDeployment(ctx, app); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("active before activation: %v", err)
	}
	prev, err := f.s.ActivateDeployment(ctx, d.ID, "w1")
	if err != nil || prev != nil {
		t.Fatalf("first activation: prev=%v err=%v", prev, err)
	}
	if o := f.op(op.ID); o.Status != store.OpSucceeded || o.LeaseOwner != "" {
		t.Fatalf("operation after activation = %+v", o)
	}

	// A second release supersedes the first in the same transaction.
	op2 := f.claimed(app, "w1")
	d2 := f.healthy(op2, "w1")
	prev, err = f.s.ActivateDeployment(ctx, d2.ID, "w1")
	if err != nil || prev == nil || prev.ID != d.ID || prev.Status != store.DeploySuperseded || prev.EndedAt == nil {
		t.Fatalf("second activation: prev=%+v err=%v", prev, err)
	}
	active, err := f.s.ActiveDeployment(ctx, app)
	if err != nil || active.ID != d2.ID || active.ActiveAt == nil {
		t.Fatalf("active = %+v, %v", active, err)
	}
	// A finished deployment accepts no more writes.
	if err := f.s.FailDeployment(ctx, d2.ID, "w1", "late"); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("fail after activation: %v", err)
	}
}

func TestDeploymentWritesNeedTheLease(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	app := f.app()
	op := f.claimed(app, "w1")

	if _, err := f.s.CreateDeployment(ctx, "w2", store.NewDeployment{OperationID: op.ID, SourceCommitSHA: testSHA}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("create by another worker: %v", err)
	}
	d, err := f.s.CreateDeployment(ctx, "w1", store.NewDeployment{OperationID: op.ID, SourceCommitSHA: testSHA})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateDeployment(ctx, "w1", store.NewDeployment{OperationID: op.ID, SourceCommitSHA: testSHA}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second deployment for one operation: %v", err)
	}
	for name, err := range map[string]error{
		"image":     f.s.RecordImage(ctx, d.ID, "w2", testImage, nil),
		"container": f.s.RecordContainer(ctx, d.ID, "w2", testContainer),
		"health":    f.s.MarkHealthChecking(ctx, d.ID, "w2"),
		"fail":      f.s.FailDeployment(ctx, d.ID, "w2", "x"),
	} {
		if !errors.Is(err, store.ErrLeaseLost) {
			t.Errorf("%s by another worker: %v", name, err)
		}
	}
	if _, err := f.s.ActivateDeployment(ctx, d.ID, "w2"); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("activate by another worker: %v", err)
	}

	// Health checking without a container breaks the schema's rule.
	if err := f.s.RecordImage(ctx, d.ID, "w1", testImage, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.s.MarkHealthChecking(ctx, d.ID, "w1"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("health_checking without container: %v", err)
	}

	// After the lease expires and is requeued, the old owner is locked out.
	f.sql(`UPDATE operations SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, op.ID)
	if _, _, err := f.s.RequeueExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.s.RecordContainer(ctx, d.ID, "w1", testContainer); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("write after requeue: %v", err)
	}
	// The retry resumes the same deployment.
	again, err := f.s.ClaimOperation(ctx, "w3", time.Minute)
	if err != nil || again.ID != op.ID {
		t.Fatalf("reclaim = %+v, %v", again, err)
	}
	if err := f.s.RecordContainer(ctx, d.ID, "w3", testContainer); err != nil {
		t.Fatal(err)
	}
}

func TestFailDeployment(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	op := f.claimed(f.app(), "w1")
	d, err := f.s.CreateDeployment(ctx, "w1", store.NewDeployment{OperationID: op.ID, SourceCommitSHA: testSHA})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.FailDeployment(ctx, d.ID, "w1", "image build failed"); err != nil {
		t.Fatal(err)
	}
	got, _ := f.s.DeploymentByID(ctx, d.ID)
	if got.Status != store.DeployFailed || got.FailureReason != "image build failed" || got.EndedAt == nil {
		t.Fatalf("failed = %+v", got)
	}
	if err := f.s.RecordImage(ctx, d.ID, "w1", testImage, nil); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("write after failure: %v", err)
	}
}
