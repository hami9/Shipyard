//go:build integration

package store_test

import (
	"errors"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

// Activation commits the verified hostnames to the candidate in the same
// transaction as the status change (ARCHITECTURE §5 step 7).
func TestActivateMovesVerifiedRoutes(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	app, other := f.app(), f.app()
	f.sql(`INSERT INTO routes (app_id, hostname) VALUES ($1, 'a.example.com'), ($1, 'b.example.com'), ($2, 'c.example.com')`, app, other)

	op := f.claimed(app, "w1")
	d := f.healthy(op, "w1")
	if err := f.s.MarkSwitching(ctx, d.ID, "w1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.DeploymentByID(ctx, d.ID); got.Status != store.DeploySwitching || got.SwitchingAt == nil {
		t.Fatalf("switching = %+v", got)
	}
	// b was added after the switch verified: it must keep its target.
	if _, err := f.s.ActivateDeployment(ctx, d.ID, "w1", "shipyard-web-1:3000", []string{"a.example.com"}); err != nil {
		t.Fatal(err)
	}
	byHost := map[string]store.Route{}
	rows, _ := f.s.ListRoutes(ctx)
	for _, r := range rows {
		byHost[r.Hostname] = r
	}
	if a := byHost["a.example.com"]; a.DeploymentID == nil || *a.DeploymentID != d.ID || a.Upstream != "shipyard-web-1:3000" {
		t.Errorf("a = %+v", a)
	}
	if b, c := byHost["b.example.com"], byHost["c.example.com"]; b.DeploymentID != nil || c.DeploymentID != nil {
		t.Errorf("unverified or foreign routes moved: b=%+v c=%+v", b, c)
	}

	// A verified hostname deleted before the commit aborts the whole commit.
	op2 := f.claimed(app, "w1")
	d2 := f.healthy(op2, "w1")
	if _, err := f.s.ActivateDeployment(ctx, d2.ID, "w1", "shipyard-web-2:3000", []string{"a.example.com", "gone.example.com"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("vanished route: %v", err)
	}
	if got, _ := f.s.DeploymentByID(ctx, d2.ID); got.Status == store.DeployActive {
		t.Fatal("the failed commit activated the deployment")
	}
	if o := f.op(op2.ID); o.Status != store.OpRunning {
		t.Fatalf("operation = %s, want still running", o.Status)
	}
	if a, _ := f.s.ActiveDeployment(ctx, app); a.ID != d.ID {
		t.Fatalf("active = %s, want the first deployment", a.ID)
	}
}

func TestListRoutes(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	if got, err := f.s.ListRoutes(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty table: %v, %v", got, err)
	}
	app := f.app()
	d := f.healthy(f.claimed(app, "w1"), "w1")
	f.sql(`INSERT INTO routes (app_id, hostname) VALUES ($1, 'z.example.com')`, app)
	f.sql(`INSERT INTO routes (app_id, hostname, deployment_id, upstream) VALUES ($1, 'a.example.com', $2, 'shipyard-web-1:3000')`, app, d.ID)

	got, err := f.s.ListRoutes(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("routes = %+v, %v", got, err)
	}
	a, z := got[0], got[1]
	if a.Hostname != "a.example.com" || a.Upstream != "shipyard-web-1:3000" || a.DeploymentID == nil || *a.DeploymentID != d.ID || a.AppID != app {
		t.Errorf("first = %+v", a)
	}
	if z.Hostname != "z.example.com" || z.Upstream != "" || z.DeploymentID != nil {
		t.Errorf("second = %+v", z)
	}
}
