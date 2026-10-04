//go:build integration

package store_test

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

const pushSHA = "0123456789abcdef0123456789abcdef01234567"

func (f *queueFixture) trackingApp(slug, repo, branch string, auto bool) string {
	f.t.Helper()
	n := minimalApp(f.u.ID, slug)
	n.RepoFullName, n.Branch, n.AutoDeploy = repo, &branch, &auto
	a, err := f.s.CreateApp(f.t.Context(), n)
	if err != nil {
		f.t.Fatal(err)
	}
	return a.ID
}

func push(id, repo, branch, sha string) store.PushDelivery {
	return store.PushDelivery{ID: id, RepositoryID: 42, Repository: repo, Ref: "refs/heads/" + branch, Branch: branch,
		After: sha, RequestID: "req-" + id}
}

func (f *queueFixture) record(d store.PushDelivery) store.PushRecord {
	f.t.Helper()
	rec, err := f.s.RecordPush(f.t.Context(), d)
	if err != nil {
		f.t.Fatalf("RecordPush %s: %v", d.ID, err)
	}
	return rec
}

func (f *queueFixture) delivery(id string) (outcome string, op *string, n int) {
	f.t.Helper()
	err := f.db.QueryRow(f.t.Context(), `SELECT coalesce(outcome, ''), operation_id::text,
		(SELECT count(*) FROM webhook_deliveries WHERE delivery_id = $1) FROM webhook_deliveries WHERE delivery_id = $1`,
		id).Scan(&outcome, &op, &n)
	if err != nil {
		f.t.Fatalf("delivery %s: %v", id, err)
	}
	return outcome, op, n
}

// P4.2: a push to a tracked branch queues one deploy of its commit; the
// same delivery again, a redelivery from GitHub, queues nothing more.
func TestRecordPush(t *testing.T) {
	f := newQueueFixture(t)
	u := uniq()
	repo := "octo/web" + u
	app := f.trackingApp("w"+u, repo, "main", true)

	rec := f.record(push("d1-"+u, strings.ToUpper(repo), "main", pushSHA)) // names match in any case
	if rec.Duplicate || rec.Outcome != store.DeliveryQueued || rec.Reason != "" || len(rec.Operations) != 1 {
		t.Fatalf("first = %+v", rec)
	}
	op := f.op(rec.Operations[0])
	if op.AppID != app || op.Kind != "deploy" || op.Status != store.OpQueued || op.IdempotencyKey != "gh:d1-"+u+":w"+u ||
		!strings.Contains(string(op.Payload), pushSHA) {
		t.Fatalf("operation = %+v %s", op, op.Payload)
	}
	if outcome, opID, _ := f.delivery("d1-" + u); outcome != store.DeliveryQueued || opID == nil || *opID != op.ID {
		t.Fatalf("delivery row = %s %v", outcome, opID)
	}

	again := f.record(push("d1-"+u, repo, "main", pushSHA))
	if !again.Duplicate || again.Outcome != store.DeliveryQueued || !slices.Equal(again.Operations, rec.Operations) {
		t.Fatalf("redelivery = %+v", again)
	}
	var ops, audits int
	f.db.QueryRow(t.Context(), `SELECT count(*) FROM operations WHERE app_id = $1`, app).Scan(&ops)
	f.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE actor = 'webhook' AND action = $1 AND target = $2
		AND result = 'success' AND request_id = $3`, store.AuditGitHubPush, "delivery:d1-"+u, "req-d1-"+u).Scan(&audits)
	if ops != 1 || audits != 1 {
		t.Fatalf("%d operations, %d audit events; want 1 and 1", ops, audits)
	}

	// A newer push replaces the still-queued deploy (coalescing).
	newer := f.record(push("d2-"+u, repo, "main", strings.Repeat("b", 40)))
	if newer.Outcome != store.DeliveryQueued || f.op(op.ID).Status != store.OpCancelled {
		t.Fatalf("newer = %+v, first now %s", newer, f.op(op.ID).Status)
	}
}

// P4.3: only apps tracking the repository and branch, with auto_deploy on,
// deploy; everything else is recorded as ignored, with the reason.
func TestRecordPushPolicy(t *testing.T) {
	f := newQueueFixture(t)
	u := uniq()
	repo := "octo/mono" + u
	api := f.trackingApp("api"+u, repo, "main", true)
	web := f.trackingApp("web"+u, repo, "main", true)
	f.trackingApp("off"+u, repo, "main", false)
	f.trackingApp("dev"+u, repo, "dev", true)
	f.trackingApp("other"+u, "octo/other"+u, "main", true)

	// Two apps share the repository and branch: one deploy each.
	rec := f.record(push("m-"+u, repo, "main", pushSHA))
	var apps []string
	for _, id := range rec.Operations {
		apps = append(apps, f.op(id).AppID)
	}
	slices.Sort(apps)
	want := []string{api, web}
	slices.Sort(want)
	if rec.Outcome != store.DeliveryQueued || !slices.Equal(apps, want) {
		t.Fatalf("shared branch = %+v (apps %v, want %v)", rec, apps, want)
	}
	if _, opID, _ := f.delivery("m-" + u); opID != nil {
		t.Fatalf("operation_id = %s with two deploys, want NULL", *opID)
	}
	if again := f.record(push("m-"+u, repo, "main", pushSHA)); !again.Duplicate || len(again.Operations) != 2 {
		t.Fatalf("redelivery of a two-app push = %+v", again)
	}

	f.sql(`UPDATE apps SET auto_deploy = false WHERE repo_full_name = $1`, "octo/other"+u)
	for name, tc := range map[string]struct {
		d      store.PushDelivery
		reason string
	}{
		"unknown repository": {push("x1-"+u, "octo/unknown"+u, "main", pushSHA), "no app deploys this repository"},
		"untracked branch":   {push("x2-"+u, repo, "feature", pushSHA), "no app tracks branch feature"},
		"auto-deploy off":    {push("x3-"+u, "octo/other"+u, "main", pushSHA), "auto-deploy is off for every app tracking this branch"},
	} {
		rec := f.record(tc.d)
		if rec.Duplicate || rec.Outcome != store.DeliveryIgnored || rec.Reason != tc.reason || len(rec.Operations) != 0 {
			t.Fatalf("%s: %+v", name, rec)
		}
		if outcome, opID, _ := f.delivery(tc.d.ID); outcome != store.DeliveryIgnored || opID != nil {
			t.Fatalf("%s: delivery row %s %v", name, outcome, opID)
		}
		if again := f.record(tc.d); !again.Duplicate || again.Outcome != store.DeliveryIgnored {
			t.Fatalf("%s: redelivery %+v", name, again)
		}
	}

	// An app being deleted takes no deploy.
	for _, id := range []string{api, web} {
		if _, err := f.s.EnqueueOperation(t.Context(), store.NewOperation{AppID: id, Kind: store.KindDelete, IdempotencyKey: "del-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	if rec := f.record(push("z-"+u, repo, "main", pushSHA)); rec.Outcome != store.DeliveryIgnored ||
		rec.Reason != "every app tracking this branch is being deleted" {
		t.Fatalf("deleting apps: %+v", rec)
	}
}

// The same delivery arriving twice at once is recorded once: the second
// waits for the first and finds it.
func TestRecordPushConcurrent(t *testing.T) {
	f := newQueueFixture(t)
	u := uniq()
	repo := "octo/race" + u
	f.trackingApp("r"+u, repo, "main", true)
	var (
		wg   sync.WaitGroup
		recs [8]store.PushRecord
		errs [8]error
	)
	for i := range recs {
		wg.Go(func() { recs[i], errs[i] = f.s.RecordPush(t.Context(), push("c-"+u, repo, "main", pushSHA)) })
	}
	wg.Wait()
	first := 0
	for i, rec := range recs {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if !rec.Duplicate {
			first++
		}
		if len(rec.Operations) != 1 || rec.Operations[0] != recs[0].Operations[0] {
			t.Fatalf("call %d: %+v vs %+v", i, rec, recs[0])
		}
	}
	if _, _, n := f.delivery("c-" + u); first != 1 || n != 1 {
		t.Fatalf("%d first records, %d rows; want 1 and 1", first, n)
	}
}

// A delivery whose deploy cannot be queued leaves nothing behind, so
// GitHub's redelivery starts over.
func TestRecordPushRollsBack(t *testing.T) {
	f := newQueueFixture(t)
	u := uniq()
	repo := "octo/rb" + u
	app := f.trackingApp("rb"+u, repo, "main", true)
	// The key is taken by another app: EnqueueOperation reports a mismatch.
	other := f.trackingApp("rx"+u, "octo/x"+u, "main", true)
	f.enqueue(other, store.WebhookKey("rb-"+u, "rb"+u))
	if _, err := f.s.RecordPush(t.Context(), push("rb-"+u, repo, "main", pushSHA)); err == nil {
		t.Fatal("RecordPush succeeded despite the key clash")
	}
	var n int
	f.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM webhook_deliveries WHERE delivery_id = $1)
		+ (SELECT count(*) FROM operations WHERE app_id = $2)
		+ (SELECT count(*) FROM audit_events WHERE target = $3)`, "rb-"+u, app, "delivery:rb-"+u).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows left behind", n)
	}
}
