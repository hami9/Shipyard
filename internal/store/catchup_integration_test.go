//go:build integration

package store_test

import (
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

// P4.5: the catch-up checks auto-deploy apps that have deployed before and
// are idle, and queues the branch head once, only if the app never tried
// that commit.
func TestCatchUp(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	u := uniq()
	activate := func(app string) {
		t.Helper()
		d := f.healthy(f.claimed(app, "w1"), "w1")
		if _, err := f.s.ActivateDeployment(ctx, d.ID, "w1", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	f.trackingApp("fresh"+u, "o/fresh"+u, "main", true) // never deployed
	off := f.trackingApp("off"+u, "o/off"+u, "main", false)
	busy := f.trackingApp("busy"+u, "o/busy"+u, "main", true)
	web := f.trackingApp("web"+u, "o/web"+u, "main", true)
	for _, a := range []string{off, busy, web} {
		activate(a) // claims take the oldest queued operation: activate first
	}
	f.enqueue(busy, "queued-"+u)
	f.sql(`UPDATE apps SET github_installation_id = 9 WHERE id = $1`, web)

	cands, err := f.s.CatchUpCandidates(ctx)
	if err != nil || len(cands) != 1 || cands[0] != (store.CatchUpApp{ID: web, Slug: "web" + u, Repo: "o/web" + u, Branch: "main", InstallationID: 9}) {
		t.Fatalf("candidates = %+v, %v", cands, err)
	}

	enqueue := func(branch, sha string) (store.Operation, bool) {
		t.Helper()
		op, created, err := f.s.EnqueueCatchUp(ctx, web, branch, sha)
		if err != nil {
			t.Fatalf("EnqueueCatchUp %s %s: %v", branch, sha, err)
		}
		return op, created
	}
	// The deployed commit is the head: nothing to do.
	if _, created := enqueue("main", testSHA); created {
		t.Fatal("queued the commit that is already deployed")
	}
	// A new head: one deploy of it.
	head := strings.Repeat("cd", 20)
	op, created := enqueue("main", head)
	if !created || op.AppID != web || op.Kind != "deploy" || op.IdempotencyKey != store.CatchUpKey(web, head) ||
		!strings.Contains(string(op.Payload), head) || op.Status != store.OpQueued {
		t.Fatalf("catch-up = %+v %s, created %v", op, op.Payload, created)
	}
	if _, created := enqueue("main", head); created {
		t.Fatal("queued the same head twice")
	}
	// Once that deploy failed, the same head is not tried again.
	f.sql(`UPDATE operations SET status = 'failed', finished_at = now() WHERE id = $1`, op.ID)
	if _, created := enqueue("main", head); created {
		t.Fatal("retried a head whose catch-up failed")
	}
	// Settings changed after the head was read: another branch, auto-deploy off.
	other := strings.Repeat("ef", 20)
	if _, created := enqueue("dev", other); created {
		t.Fatal("queued a head of a branch the app no longer tracks")
	}
	f.sql(`UPDATE apps SET auto_deploy = false WHERE id = $1`, web)
	if _, created := enqueue("main", other); created {
		t.Fatal("queued with auto-deploy off")
	}
	f.sql(`UPDATE apps SET auto_deploy = true WHERE id = $1`, web)
	// An app being deleted takes nothing.
	if _, err := f.s.EnqueueOperation(ctx, store.NewOperation{AppID: web, Kind: store.KindDelete, IdempotencyKey: "del-" + u}); err != nil {
		t.Fatal(err)
	}
	if _, created := enqueue("main", other); created {
		t.Fatal("queued a deploy of an app being deleted")
	}
	if cands, _ := f.s.CatchUpCandidates(ctx); len(cands) != 0 {
		t.Fatalf("candidates while deleting = %+v", cands)
	}
}
