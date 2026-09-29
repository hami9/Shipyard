//go:build integration

package store_test

import (
	"errors"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

// P2.5: a new hostname targets the app's active deployment at once, reusing
// the upstream its other routes use; duplicates conflict; delete is exact.
func TestCreateRoute(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	app := f.app()
	calls := 0
	up := func(dep string) string { calls++; return "shipyard-web-" + dep + ":3000" }

	// No active deployment yet: no target.
	r, err := f.s.CreateRoute(ctx, store.NewRoute{AppID: app, Hostname: "a.example.com"}, up)
	if err != nil || r.DeploymentID != nil || r.Upstream != "" || r.AppID != app || calls != 0 {
		t.Fatalf("first route = %+v, %v (calls %d)", r, err, calls)
	}
	if _, err := f.s.CreateRoute(ctx, store.NewRoute{AppID: f.app(), Hostname: "a.example.com"}, up); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate hostname on another app: %v", err)
	}

	// Activate a deployment; a routes-less app's first route computes the upstream.
	d := f.healthy(f.claimed(app, "w1"), "w1")
	if _, err := f.s.ActivateDeployment(ctx, d.ID, "w1", "", nil); err != nil {
		t.Fatal(err)
	}
	b, err := f.s.CreateRoute(ctx, store.NewRoute{AppID: app, Hostname: "b.example.com"}, up)
	if err != nil || b.DeploymentID == nil || *b.DeploymentID != d.ID || b.Upstream != "shipyard-web-"+d.ID+":3000" || calls != 1 {
		t.Fatalf("route on the active deployment = %+v, %v (calls %d)", b, err, calls)
	}
	// Once a route carries the committed upstream, new ones reuse it.
	f.sql(`UPDATE routes SET upstream = 'committed:8080' WHERE hostname = 'b.example.com'`)
	c, err := f.s.CreateRoute(ctx, store.NewRoute{AppID: app, Hostname: "c.example.com"}, up)
	if err != nil || c.Upstream != "committed:8080" || calls != 1 {
		t.Fatalf("reused upstream = %+v, %v (calls %d)", c, err, calls)
	}

	rows, _ := f.s.RoutesByApp(ctx, app)
	if len(rows) != 3 || rows[0].Hostname != "a.example.com" || rows[2].Hostname != "c.example.com" {
		t.Fatalf("RoutesByApp = %+v", rows)
	}
	if err := f.s.DeleteRoute(ctx, app, "c.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteRoute(ctx, app, "c.example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := f.s.DeleteRoute(ctx, f.app(), "a.example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete through another app: %v", err)
	}
}

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
	// b was added after the switch verified; with no deployment it follows
	// the app to the new active one. The other app's route never moves.
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
	if b := byHost["b.example.com"]; b.DeploymentID == nil || *b.DeploymentID != d.ID || b.Upstream != "shipyard-web-1:3000" {
		t.Errorf("a route without a deployment did not follow: b=%+v", b)
	}
	if c := byHost["c.example.com"]; c.DeploymentID != nil {
		t.Errorf("another app's route moved: c=%+v", c)
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
