//go:build integration

package store_test

import (
	"testing"
)

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
