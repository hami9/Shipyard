package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/applogs"
	"github.com/hami9/shipyard/internal/build"
	"github.com/hami9/shipyard/internal/health"
	"github.com/hami9/shipyard/internal/runtime"
	"github.com/hami9/shipyard/internal/source"
)

// The deploy use case declares its ports (internal/app); these adapters
// connect them to the worker-only packages. internal/app cannot import them:
// internal/source imports internal/app, and the API binary must not link the
// Docker client.

type sourceAdapter struct{ f *source.Fetcher }

func (a sourceAdapter) Fetch(ctx context.Context, r app.FetchRequest) (app.Fetched, error) {
	co, err := a.f.Fetch(ctx, source.Request{OperationID: r.OperationID, Repo: r.Repo, Branch: r.Branch, Ref: r.Ref})
	if err != nil {
		return app.Fetched{}, err
	}
	dockerfile, err := co.Path(r.Dockerfile)
	if err != nil {
		return app.Fetched{}, err
	}
	buildContext, err := co.Path(r.Context)
	if err != nil {
		return app.Fetched{}, err
	}
	return app.Fetched{SHA: co.SHA, Dockerfile: dockerfile, Context: buildContext}, nil
}

func (a sourceAdapter) Cleanup(opID string) error { return a.f.Cleanup(opID) }

type buildAdapter struct{ b *build.Builder }

func (a buildAdapter) Build(ctx context.Context, r app.BuildRequest) (app.Image, error) {
	res, err := a.b.Build(ctx, build.Request{Dockerfile: r.Dockerfile, Context: r.Context, App: r.App,
		Commit: r.Commit, DeploymentID: r.DeploymentID, Log: r.Log})
	if err != nil {
		return app.Image{}, err
	}
	return app.Image{ID: res.ImageID, Metadata: res.Metadata}, nil
}

type runtimeAdapter struct{ r *runtime.Runtime }

func (a runtimeAdapter) Create(ctx context.Context, c app.Container) (string, error) {
	return a.r.Create(ctx, runtime.Spec{App: c.App, DeploymentID: c.DeploymentID, Commit: c.Commit, Image: c.Image,
		Env: c.Env, CPUs: c.CPUs, Memory: c.Memory, StopTimeout: c.StopTimeout})
}

func (a runtimeAdapter) Start(ctx context.Context, id string) error { return a.r.Start(ctx, id) }

func (a runtimeAdapter) Inspect(ctx context.Context, id string) (app.ContainerState, error) {
	st, err := a.r.Inspect(ctx, id)
	if errors.Is(err, runtime.ErrNotFound) {
		return app.ContainerState{}, fmt.Errorf("%w: %w", app.ErrContainerGone, err)
	}
	if err != nil {
		return app.ContainerState{}, err
	}
	return app.ContainerState{Name: st.Name, Running: st.Running, Restarting: st.Restarting, OOMKilled: st.OOMKilled,
		ExitCode: st.ExitCode, RestartCount: st.RestartCount, IP: st.IP}, nil
}

func (a runtimeAdapter) Stop(ctx context.Context, id string, timeout time.Duration) error {
	return a.r.Stop(ctx, id, timeout)
}

func (a runtimeAdapter) Remove(ctx context.Context, id string) error { return a.r.Remove(ctx, id) }

func (a runtimeAdapter) Logs(ctx context.Context, id string, tail int) ([]string, error) {
	return a.r.Logs(ctx, id, tail)
}

func (a runtimeAdapter) ImageExists(ctx context.Context, id string) (bool, error) {
	return a.r.ImageExists(ctx, id)
}

func (a runtimeAdapter) ListImages(ctx context.Context) ([]string, error) { return a.r.ListImages(ctx) }

func (a runtimeAdapter) RemoveImage(ctx context.Context, id string) error {
	err := a.r.RemoveImage(ctx, id)
	if errors.Is(err, runtime.ErrInUse) {
		return fmt.Errorf("%w: %w", app.ErrImageInUse, err)
	}
	return err
}

func (a runtimeAdapter) StreamLogs(ctx context.Context, id string, tail int, follow bool, fn func(applogs.Line) error) error {
	return a.r.StreamLogs(ctx, id, tail, follow, func(l runtime.LogLine) error {
		return fn(applogs.Line{TS: l.TS, Stream: l.Stream, Text: l.Text})
	})
}

func (a runtimeAdapter) ListManaged(ctx context.Context) ([]app.ManagedContainer, error) {
	list, err := a.r.ListManaged(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]app.ManagedContainer, len(list))
	for i, c := range list {
		out[i] = app.ManagedContainer{ID: c.ID, App: c.App, DeploymentID: c.DeploymentID, Running: c.Running}
	}
	return out, nil
}

// edgeData is the Caddy container's data volume, for backups.
type edgeData struct {
	r    *runtime.Runtime
	name string
}

func (e edgeData) ArchiveData(ctx context.Context, w io.Writer) error {
	return e.r.ArchiveEdgeData(ctx, e.name, w)
}

func healthGate(ctx context.Context, url string, timeout time.Duration, alive func(context.Context) error) error {
	return health.Wait(ctx, health.Config{URL: url, Timeout: timeout}, alive)
}
