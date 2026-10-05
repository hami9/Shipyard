package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// fakes records every call in order, across all ports.
type fakes struct {
	calls      []string
	deps       []store.Deployment
	states     map[string]app.ContainerState // missing: gone
	noImage    map[string]bool               // image IDs gone from the host
	rebuilds   map[string]store.Operation    // by deployment: an earlier rebuild
	rebuildErr error
	createErr  error
	replaceErr error
	syncErr    error
	depsErr    error
}

func (f *fakes) ImageExists(_ context.Context, id string) (bool, error) { return !f.noImage[id], nil }
func (f *fakes) EnqueueRebuild(_ context.Context, id string, payload json.RawMessage) (store.Operation, bool, error) {
	f.call("rebuild %s %s", id, payload)
	if f.rebuildErr != nil {
		return store.Operation{}, false, f.rebuildErr
	}
	if op, ok := f.rebuilds[id]; ok {
		return op, false, nil
	}
	return store.Operation{ID: "op-" + id, Status: store.OpQueued}, true, nil
}

func (f *fakes) call(format string, a ...any) { f.calls = append(f.calls, fmt.Sprintf(format, a...)) }

func (f *fakes) RequeueExpired(context.Context) (int, int, error) {
	f.call("requeue")
	return 0, 0, nil
}
func (f *fakes) ActiveDeployments(context.Context) ([]store.Deployment, error) {
	return f.deps, f.depsErr
}
func (f *fakes) AppByID(_ context.Context, id string) (store.App, error) {
	return store.App{ID: id, Slug: "web-" + id, CPULimit: 0.5, MemoryLimit: 64 << 20, StopTimeout: 7 * time.Second,
		InternalPort: 8080, HealthPath: "/healthz"}, nil
}
func (f *fakes) ReplaceContainer(_ context.Context, id, old, c string) error {
	f.call("record %s %s->%s", id, old, c)
	return f.replaceErr
}
func (f *fakes) Inspect(_ context.Context, id string) (app.ContainerState, error) {
	st, ok := f.states[id]
	if !ok {
		return app.ContainerState{}, fmt.Errorf("inspect %s: %w", id, app.ErrContainerGone)
	}
	return st, nil
}
func (f *fakes) Create(_ context.Context, c app.Container) (string, error) {
	f.call("create %s %s %s env=%v cpus=%v stop=%s", c.App, c.DeploymentID, c.Image, c.Env, c.CPUs, c.StopTimeout)
	if f.createErr != nil {
		return "", f.createErr
	}
	return "new-" + c.DeploymentID, nil
}
func (f *fakes) Start(_ context.Context, id string) error { f.call("start %s", id); return nil }
func (f *fakes) Resolve(_ context.Context, rev string) (map[string]string, error) {
	return map[string]string{"REV": rev}, nil
}
func (f *fakes) Sync(context.Context) (bool, error) { f.call("sync"); return false, f.syncErr }
func (f *fakes) Sweep(context.Context, time.Time) ([]string, error) {
	f.call("sweep")
	return nil, nil
}
func (f *fakes) Prune(context.Context) ([]string, error) { f.call("prune"); return nil, nil }

func newReconciler(f *fakes) (*Reconciler, *strings.Builder) {
	var logs strings.Builder
	return &Reconciler{Queue: f, Store: f, Runtime: f, Env: f, Router: f, Janitor: f, Images: f,
		Log: slog.New(slog.NewTextHandler(&logs, nil))}, &logs
}

// P3.2: one pass requeues, restores active containers, syncs Caddy, then
// sweeps, then (P3.4) prunes images; a gone container is recreated from its image and environment and
// recorded before it starts; a stopped one is started; a serving or
// restarting one is left alone.
func TestPass(t *testing.T) {
	rev := "rev-1"
	f := &fakes{
		deps: []store.Deployment{
			{ID: "d1", AppID: "a1", ContainerID: "c-running", ImageID: "sha256:1"},
			{ID: "d2", AppID: "a2", ContainerID: "c-stopped", ImageID: "sha256:2"},
			{ID: "d3", AppID: "a3", ContainerID: "c-gone", ImageID: "sha256:3", EnvRevisionID: &rev},
			{ID: "d4", AppID: "a4", ContainerID: "c-restarting", ImageID: "sha256:4"},
		},
		states: map[string]app.ContainerState{
			"c-running": {Running: true}, "c-stopped": {ExitCode: 137}, "c-restarting": {Restarting: true},
		},
	}
	r, logs := newReconciler(f)
	r.Pass(t.Context())
	want := []string{
		"requeue",
		"start c-stopped",
		"create web-a3 d3 sha256:3 env=map[REV:rev-1] cpus=0.5 stop=7s",
		"record d3 c-gone->new-d3",
		"start new-d3",
		"sync",
		"sweep",
		"prune",
	}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls =\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(logs.String(), "active container was gone; recreated it") {
		t.Fatalf("logs:\n%s", logs)
	}
}

// A failing item does not stop the pass: the image is gone (recreate
// fails), or a deploy superseded the deployment meanwhile (the recorded
// container is not started; the janitor removes it). Caddy disabled means
// no sync.
func TestPassCarriesOn(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*fakes)
		want   []string
		log    string
	}{
		"image gone": {
			mutate: func(f *fakes) { f.createErr = errors.New("No such image: sha256:3") },
			want:   []string{"requeue", "create web-a3 d3 sha256:3 env=map[] cpus=0.5 stop=7s", "sweep", "prune"},
			log:    "No such image",
		},
		"superseded meanwhile": {
			mutate: func(f *fakes) { f.replaceErr = store.ErrConflict },
			want:   []string{"requeue", "create web-a3 d3 sha256:3 env=map[] cpus=0.5 stop=7s", "record d3 c-gone->new-d3", "sweep", "prune"},
			log:    "record recreated container",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakes{deps: []store.Deployment{{ID: "d3", AppID: "a3", ContainerID: "c-gone", ImageID: "sha256:3"}}}
			tc.mutate(f)
			r, logs := newReconciler(f)
			r.Router = nil
			r.Pass(t.Context())
			if !slices.Equal(f.calls, tc.want) {
				t.Fatalf("calls = %q, want %q", f.calls, tc.want)
			}
			if !strings.Contains(logs.String(), "restoring active containers incomplete") || !strings.Contains(logs.String(), tc.log) {
				t.Fatalf("logs:\n%s", logs)
			}
		})
	}
}

// P3.6a: an active deployment whose container and image are both gone (a
// restore onto a fresh host) is rebuilt by one deploy of its commit. A
// rebuild in progress is left alone; a failed one is reported on every
// pass; an app with another operation in progress is left to it.
func TestPassRebuilds(t *testing.T) {
	const rebuild = `rebuild d3 {"rebuild_of":"d3"}`
	for name, tc := range map[string]struct {
		mutate func(*fakes)
		log    string // "": the pass is quiet about this deployment
	}{
		"requested":     {func(*fakes) {}, "active container and image are gone; rebuilding the commit"},
		"queued":        {func(f *fakes) { f.rebuilds = map[string]store.Operation{"d3": {ID: "op-1", Status: store.OpQueued}} }, ""},
		"running":       {func(f *fakes) { f.rebuilds = map[string]store.Operation{"d3": {ID: "op-1", Status: store.OpRunning}} }, ""},
		"failed":        {func(f *fakes) { f.rebuilds = map[string]store.Operation{"d3": {ID: "op-1", Status: store.OpFailed}} }, "its rebuild (operation op-1) failed: deploy commit abc or roll back"},
		"app busy":      {func(f *fakes) { f.rebuildErr = fmt.Errorf("%w: busy", store.ErrConflict) }, ""},
		"database down": {func(f *fakes) { f.rebuildErr = errors.New("connection refused") }, "request rebuild: connection refused"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakes{noImage: map[string]bool{"sha256:3": true},
				deps: []store.Deployment{{ID: "d3", AppID: "a3", ContainerID: "c-gone", ImageID: "sha256:3", SourceCommitSHA: "abc"}}}
			tc.mutate(f)
			r, logs := newReconciler(f)
			r.Pass(t.Context())
			if want := []string{"requeue", rebuild, "sync", "sweep", "prune"}; !slices.Equal(f.calls, want) {
				t.Fatalf("calls = %q, want %q", f.calls, want)
			}
			if tc.log == "" && strings.Contains(logs.String(), "d3") {
				t.Fatalf("logs:\n%s", logs)
			}
			if !strings.Contains(logs.String(), tc.log) {
				t.Fatalf("logs lack %q:\n%s", tc.log, logs)
			}
		})
	}
}

type healthLog struct{ reports [][]AppHealth }

func (h *healthLog) ReportHealth(apps []AppHealth) { h.reports = append(h.reports, apps) }

// P5.5b: after the restore, each active app is reported running or not,
// and healthy when one GET of its health path passes; a recreated container
// is checked under its new ID. A pass that cannot list the deployments
// reports nothing; one with none reports an empty list.
func TestPassReportsHealth(t *testing.T) {
	ip := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	f := &fakes{
		deps: []store.Deployment{
			{ID: "d1", AppID: "a1", ContainerID: "c1", ImageID: "sha256:1"},
			{ID: "d2", AppID: "a2", ContainerID: "c2", ImageID: "sha256:2"},
			{ID: "d3", AppID: "a3", ContainerID: "c-gone", ImageID: "sha256:3"},
			{ID: "d4", AppID: "a4", ContainerID: "c-stopped", ImageID: "sha256:4"},
		},
		states: map[string]app.ContainerState{
			"c1": {Running: true, IP: ip("10.0.0.1")}, "c2": {Running: true, IP: ip("10.0.0.2")},
			"new-d3": {Running: true, IP: ip("10.0.0.3")}, "c-stopped": {ExitCode: 1},
		},
	}
	r, _ := newReconciler(f)
	var probed []string
	r.Probe = func(_ context.Context, url string) error {
		probed = append(probed, url)
		if strings.Contains(url, "10.0.0.2") {
			return errors.New("status 503")
		}
		return nil
	}
	h := &healthLog{}
	r.Health = h
	r.Pass(t.Context())
	want := []AppHealth{{"web-a1", true, true}, {"web-a2", true, false}, {"web-a3", true, true}, {"web-a4", false, false}}
	if len(h.reports) != 1 || !slices.Equal(h.reports[0], want) {
		t.Fatalf("reports = %+v, want [%+v]", h.reports, want)
	}
	if wantURLs := []string{"http://10.0.0.1:8080/healthz", "http://10.0.0.2:8080/healthz", "http://10.0.0.3:8080/healthz"}; !slices.Equal(probed, wantURLs) {
		t.Fatalf("probed %q, want %q", probed, wantURLs)
	}

	f.depsErr = errors.New("connection refused")
	r.Pass(t.Context())
	f.depsErr, f.deps = nil, nil
	r.Pass(t.Context())
	if len(h.reports) != 2 || h.reports[1] == nil || len(h.reports[1]) != 0 {
		t.Fatalf("reports = %+v, want one more, empty", h.reports)
	}
}

// Run passes at once, then every interval, until the context ends.
func TestRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakes{}
		r, _ := newReconciler(f)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { r.Run(ctx, time.Minute); close(done) }()
		time.Sleep(3*time.Minute + time.Second)
		synctest.Wait()
		cancel()
		<-done
		if n := strings.Count(strings.Join(f.calls, ","), "requeue"); n != 4 {
			t.Fatalf("%d passes in 3m at 1m, want 4 (at 0, 1, 2, 3m)", n)
		}
	})
}
