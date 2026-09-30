package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	createErr  error
	replaceErr error
	syncErr    error
}

func (f *fakes) call(format string, a ...any) { f.calls = append(f.calls, fmt.Sprintf(format, a...)) }

func (f *fakes) RequeueExpired(context.Context) (int, int, error) {
	f.call("requeue")
	return 0, 0, nil
}
func (f *fakes) ActiveDeployments(context.Context) ([]store.Deployment, error) {
	return f.deps, nil
}
func (f *fakes) AppByID(_ context.Context, id string) (store.App, error) {
	return store.App{ID: id, Slug: "web-" + id, CPULimit: 0.5, MemoryLimit: 64 << 20, StopTimeout: 7 * time.Second}, nil
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

func newReconciler(f *fakes) (*Reconciler, *strings.Builder) {
	var logs strings.Builder
	return &Reconciler{Queue: f, Store: f, Runtime: f, Env: f, Router: f, Janitor: f,
		Log: slog.New(slog.NewTextHandler(&logs, nil))}, &logs
}

// P3.2: one pass requeues, restores active containers, syncs Caddy, then
// sweeps; a gone container is recreated from its image and environment and
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
			want:   []string{"requeue", "create web-a3 d3 sha256:3 env=map[] cpus=0.5 stop=7s", "sweep"},
			log:    "No such image",
		},
		"superseded meanwhile": {
			mutate: func(f *fakes) { f.replaceErr = store.ErrConflict },
			want:   []string{"requeue", "create web-a3 d3 sha256:3 env=map[] cpus=0.5 stop=7s", "record d3 c-gone->new-d3", "sweep"},
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
