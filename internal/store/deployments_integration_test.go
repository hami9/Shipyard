//go:build integration

package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
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

// P3.3: a rollback deployment copies its source's commit and image, links
// it, pins the chosen revision, and starts in status starting. A source of
// another app, or without an image, is refused.
func TestCreateRollbackDeployment(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	app, other := f.app(), f.app()
	src := f.healthy(f.claimed(app, "w1"), "w1")
	if _, err := f.s.ActivateDeployment(ctx, src.ID, "w1", "", nil); err != nil {
		t.Fatal(err)
	}
	src, _ = f.s.DeploymentByID(ctx, src.ID)

	f.enqueue(app, "rb-"+uniq()) // a rollback operation, claimed
	op, _ := f.s.ClaimOperation(ctx, "w1", time.Minute)
	d, err := f.s.CreateRollbackDeployment(ctx, "w1", store.NewRollback{OperationID: op.ID, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "rollback" || d.SourceDeployment == nil || *d.SourceDeployment != src.ID || d.ImageID != testImage ||
		d.SourceCommitSHA != testSHA || d.Status != store.DeployStarting || d.StartingAt == nil || d.EnvRevisionID != nil || d.ContainerID != "" {
		t.Fatalf("rollback deployment = %+v", d)
	}
	if got, _ := f.s.Releases(ctx, app, "", 1); got[0].ID != d.ID || *got[0].SourceDeployment != src.ID {
		t.Fatalf("history head = %+v", got[0])
	}

	// Another app's deployment, or none built, cannot be a source.
	f.enqueue(other, "rb-"+uniq())
	op2, _ := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if _, err := f.s.CreateRollbackDeployment(ctx, "w1", store.NewRollback{OperationID: op2.ID, Source: src}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("cross-app source: %v", err)
	}
	unbuilt, _ := f.s.CreateDeployment(ctx, "w1", store.NewDeployment{OperationID: op2.ID, SourceCommitSHA: testSHA})
	f.enqueue(other, "rb-"+uniq())
	f.sql(`UPDATE operations SET status = 'succeeded', finished_at = now(), lease_owner = NULL, lease_expires_at = NULL WHERE id = $1`, op2.ID)
	op3, _ := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if _, err := f.s.CreateRollbackDeployment(ctx, "w1", store.NewRollback{OperationID: op3.ID, Source: unbuilt}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("unbuilt source: %v", err)
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

// P3.4: retention keeps each app's active image, the images of its last
// keep superseded releases (an image served twice counts once, at its
// latest), images of deployments in progress, and a pending rollback's
// target. It returns only images a deployment here recorded.
func TestPrunableImages(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	img := func(n int) string { return "sha256:" + strings.Repeat(fmt.Sprintf("%02x", n), 32) }
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	dep := func(app, status string, image, hour int) string {
		t.Helper()
		var opID, id string
		if err := f.db.QueryRow(ctx, `INSERT INTO operations (app_id, kind, idempotency_key, status, finished_at)
			VALUES ($1, 'deploy', $2, 'succeeded', now()) RETURNING id`, app, "k-"+uniq()).Scan(&opID); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(ctx, `INSERT INTO deployments (app_id, operation_id, kind, source_commit_sha, image_id,
				container_id, status, active_at, failure_reason)
			VALUES ($1, $2, 'build', $3, $4, $5, $6, $7, CASE WHEN $6 = 'failed' THEN 'x' END) RETURNING id`,
			app, opID, testSHA, img(image), testContainer, status, base.Add(time.Duration(hour)*time.Hour)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := f.app(), f.app()
	first := dep(a, "superseded", 1, 1)
	dep(a, "superseded", 2, 2)
	dep(a, "superseded", 3, 3)
	dep(a, "superseded", 2, 4) // image 2 served again (a rollback), latest
	dep(a, "active", 5, 5)
	dep(a, "superseded", 5, 0) // the active image, served before too
	dep(a, "failed", 6, 6)
	dep(a, "building", 7, 7)
	dep(b, "superseded", 8, 0) // another app's last release
	// Image 9 is not recorded here.
	present := []string{img(1), img(2), img(3), img(5), img(6), img(7), img(8), img(9)}

	check := func(present []string, keep int, want ...int) {
		t.Helper()
		got, err := f.s.PrunableImages(ctx, present, keep)
		var wantIDs []string
		for _, n := range want {
			wantIDs = append(wantIDs, img(n))
		}
		if err != nil || strings.Join(got, ",") != strings.Join(wantIDs, ",") {
			t.Fatalf("keep %d: prunable = %v, %v; want images %v", keep, got, err, want)
		}
	}
	check(present, 2, 1, 6)
	check(present, 0, 1, 2, 3, 6, 8)
	check(present, 5, 6)
	check([]string{img(3), img(9)}, 2)    // only what is present
	check([]string{img(1), img(9)}, 2, 1) // an unknown image never

	// A queued rollback to the first release keeps its image.
	f.sql(`INSERT INTO operations (app_id, kind, idempotency_key, payload)
		VALUES ($1, 'rollback', $2, jsonb_build_object('target', $3::text))`, a, "rb-"+uniq(), first)
	check(present, 2, 6)
}
