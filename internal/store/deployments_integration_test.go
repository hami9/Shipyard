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

// P3.1: the history lists an app's deployments newest first, pages with a
// stable cursor, and names each environment revision by number.
func TestReleases(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	app, other := f.app(), f.app()
	if got, err := f.s.Releases(ctx, app, "", 10); err != nil || len(got) != 0 {
		t.Fatalf("empty history = %v, %v", got, err)
	}
	var ids []string // oldest first
	for i := range 3 {
		op := f.claimed(app, "w1")
		d := f.healthy(op, "w1")
		if i < 2 {
			if _, err := f.s.ActivateDeployment(ctx, d.ID, "w1", "", nil); err != nil {
				t.Fatal(err)
			}
		} else if err := f.s.FailDeployment(ctx, d.ID, "w1", "health check did not pass"); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	f.healthy(f.claimed(other, "w1"), "w1") // another app's: never listed

	all, err := f.s.Releases(ctx, app, "", 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("history = %d, %v", len(all), err)
	}
	var got []string
	for _, r := range all {
		got = append(got, r.ID[:8]+" "+r.Status)
	}
	want := []string{ids[2][:8] + " failed", ids[1][:8] + " active", ids[0][:8] + " superseded"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("history = %v, want %v", got, want)
	}
	if all[0].FailureReason != "health check did not pass" || all[1].EnvRevision != 0 || all[2].ImageID != testImage {
		t.Fatalf("details = %+v", all)
	}

	// Pages: two, then the rest after the second's ID.
	page, _ := f.s.Releases(ctx, app, "", 2)
	rest, err := f.s.Releases(ctx, app, page[1].ID, 2)
	if len(page) != 2 || err != nil || len(rest) != 1 || rest[0].ID != ids[0] {
		t.Fatalf("pages = %d then %d (%v)", len(page), len(rest), err)
	}
	// A cursor from another app, or unknown, is not found.
	otherRel, _ := f.s.Releases(ctx, other, "", 1)
	for _, before := range []string{otherRel[0].ID, "00000000-0000-4000-8000-000000000000"} {
		if _, err := f.s.Releases(ctx, app, before, 2); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("before %s: %v", before, err)
		}
	}
}

// P3.2: the reconciler finds every active deployment and records a
// recreated container only while the deployment is still active with the
// container it replaced.
func TestReplaceContainer(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	a, b := f.app(), f.app()
	da := f.healthy(f.claimed(a, "w1"), "w1")
	db := f.healthy(f.claimed(b, "w1"), "w1")
	for _, d := range []store.Deployment{da, db} {
		if _, err := f.s.ActivateDeployment(ctx, d.ID, "w1", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	f.healthy(f.claimed(a, "w1"), "w1") // in progress: not active
	active, err := f.s.ActiveDeployments(ctx)
	if err != nil || len(active) != 2 || active[0].Status != store.DeployActive || active[1].Status != store.DeployActive {
		t.Fatalf("active = %+v, %v", active, err)
	}

	next := strings.Repeat("d2", 32)
	if err := f.s.ReplaceContainer(ctx, da.ID, testContainer, next); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.DeploymentByID(ctx, da.ID); got.ContainerID != next || got.Status != store.DeployActive {
		t.Fatalf("after replace = %+v", got)
	}
	// A stale view (the old container again) or a deployment that is no
	// longer active changes nothing.
	if err := f.s.ReplaceContainer(ctx, da.ID, testContainer, strings.Repeat("e3", 32)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale replace: %v", err)
	}
	f.sql(`UPDATE deployments SET status = 'superseded', ended_at = now() WHERE id = $1`, db.ID)
	if err := f.s.ReplaceContainer(ctx, db.ID, testContainer, next); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("superseded replace: %v", err)
	}
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
	prev, err := f.s.ActivateDeployment(ctx, d.ID, "w1", "", nil)
	if err != nil || prev != nil {
		t.Fatalf("first activation: prev=%v err=%v", prev, err)
	}
	if o := f.op(op.ID); o.Status != store.OpSucceeded || o.LeaseOwner != "" {
		t.Fatalf("operation after activation = %+v", o)
	}

	// A second release supersedes the first in the same transaction.
	op2 := f.claimed(app, "w1")
	d2 := f.healthy(op2, "w1")
	prev, err = f.s.ActivateDeployment(ctx, d2.ID, "w1", "", nil)
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
	if _, err := f.s.ActivateDeployment(ctx, d.ID, "w2", "", nil); !errors.Is(err, store.ErrLeaseLost) {
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
