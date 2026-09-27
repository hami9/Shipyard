package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// Operation phases of a deploy. Each is persisted before its side effect
// (invariant 3), so a retried operation knows where it stopped.
const (
	PhaseFetch    = "fetch"
	PhaseBuild    = "build"
	PhaseStart    = "start"
	PhaseHealth   = "health"
	PhaseActivate = "activate"
)

// DeployPayload is a deploy operation's payload, written by the API.
type DeployPayload struct {
	Ref string `json:"ref,omitempty"` // full SHA; empty: the tracked branch's head at fetch time
}

// DeployStore is what the deploy use case needs from persistence.
type DeployStore interface {
	AppByID(ctx context.Context, id string) (store.App, error)
	LatestEnvRevision(ctx context.Context, appID string) (store.EnvRevision, error)
	SetOperationPhase(ctx context.Context, id, owner, phase string) error
	FailOperation(ctx context.Context, id, owner, reason string, retryAfter time.Duration) (string, error)
	AppendOperationEvent(ctx context.Context, opID, level, message string) (store.OperationEvent, error)
	CreateDeployment(ctx context.Context, owner string, n store.NewDeployment) (store.Deployment, error)
	DeploymentByOperation(ctx context.Context, opID string) (store.Deployment, error)
	RecordImage(ctx context.Context, id, owner, imageID string, metadata json.RawMessage) error
	RecordContainer(ctx context.Context, id, owner, containerID string) error
	MarkHealthChecking(ctx context.Context, id, owner string) error
	FailDeployment(ctx context.Context, id, owner, reason string) error
	ActivateDeployment(ctx context.Context, id, owner string) (*store.Deployment, error)
}

// Source puts the commit into the operation's workspace (internal/source).
type Source interface {
	Fetch(ctx context.Context, r FetchRequest) (Fetched, error)
	Cleanup(operationID string) error
}

// FetchRequest names the commit and the app's repository-relative paths.
type FetchRequest struct {
	OperationID, Repo, Branch string
	Ref                       string // full SHA; empty: the branch head
	Dockerfile, Context       string
}

// Fetched is a verified checkout: the SHA is on the tracked branch
// (invariant 7), and the paths are absolute and resolved inside it.
type Fetched struct {
	SHA                 string
	Dockerfile, Context string
}

// Builder builds an image (internal/build).
type Builder interface {
	Build(ctx context.Context, r BuildRequest) (Image, error)
}

// BuildRequest is one build; Log receives bounded build output.
type BuildRequest struct {
	Dockerfile, Context, App, Commit, DeploymentID string
	Log                                            func(line string)
}

// Image is a built image: its Engine ID is the release identity (ADR-0004).
type Image struct {
	ID       string
	Metadata json.RawMessage
}

// Runtime runs containers (internal/runtime). Every call is idempotent.
type Runtime interface {
	Create(ctx context.Context, c Container) (string, error)
	Start(ctx context.Context, id string) error
	Inspect(ctx context.Context, id string) (ContainerState, error)
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Remove(ctx context.Context, id string) error
	Logs(ctx context.Context, id string, tail int) ([]string, error)
}

// Container is a deployment's container; the runtime adds the hardening.
type Container struct {
	App, DeploymentID, Commit, Image string
	Env                              map[string]string
	CPUs                             float64
	Memory                           int64
	StopTimeout                      time.Duration
}

// ContainerState is what the health gate watches.
type ContainerState struct {
	Running, Restarting, OOMKilled bool
	ExitCode, RestartCount         int
	IP                             netip.Addr
}

// EnvResolver decrypts an environment revision (internal/secrets).
type EnvResolver interface {
	Resolve(ctx context.Context, revisionID string) (map[string]string, error)
}

// HealthGate waits for the candidate to pass (internal/health); alive
// reports a container that stopped or restarted.
type HealthGate func(ctx context.Context, url string, timeout time.Duration, alive func(context.Context) error) error

// Deployer runs deploy operations (ARCHITECTURE §5): fetch, build, start,
// health-check, activate. Routing arrives in Phase 2, so an active
// deployment is not yet reachable from outside.
type Deployer struct {
	Store   DeployStore
	Source  Source
	Builder Builder
	Runtime Runtime
	Env     EnvResolver
	Health  HealthGate
	Log     *slog.Logger
}

// logTail is how many lines of a failed candidate's output are kept.
const logTail = 50

// Run executes a claimed deploy operation. It returns nil once the operation
// reached a final, recorded state: succeeded, or failed with a reason.
//
// It returns an error only when it could not record an outcome: the lease was
// lost, ctx ended (shutdown), or the database failed. The lease then expires
// and a later attempt resumes the same deployment, skipping the steps whose
// results are recorded.
func (d *Deployer) Run(ctx context.Context, op store.Operation, owner string) error {
	r := &deployRun{Deployer: d, op: op, owner: owner,
		log: d.Log.With(slog.String("operation_id", op.ID), slog.String("app_id", op.AppID))}
	err := r.execute(ctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrLeaseLost) || ctx.Err() != nil {
		r.log.Warn("deploy interrupted; it resumes after the lease expires", slog.Any("err", err))
		return err
	}
	return r.fail(ctx, err)
}

type deployRun struct {
	*Deployer
	op    store.Operation
	owner string
	log   *slog.Logger
	app   store.App
	dep   store.Deployment
}

func (r *deployRun) execute(ctx context.Context) error {
	var err error
	if r.app, err = r.Store.AppByID(ctx, r.op.AppID); err != nil {
		return fmt.Errorf("load app: %w", err)
	}
	var p DeployPayload
	if len(r.op.Payload) > 0 {
		if err := json.Unmarshal(r.op.Payload, &p); err != nil {
			return fmt.Errorf("invalid deploy payload: %w", err)
		}
	}
	r.dep, err = r.Store.DeploymentByOperation(ctx, r.op.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return fmt.Errorf("load deployment: %w", err)
	case r.dep.Status == store.DeployFailed:
		return errors.New(r.dep.FailureReason) // failed, but the operation was not closed
	default:
		r.log = r.log.With(slog.String("deployment_id", r.dep.ID))
		r.event(ctx, store.LevelInfo, "resuming deployment %s (attempt %d)", r.dep.ID, r.op.Attempt)
	}

	if r.dep.ImageID == "" {
		if err := r.fetchAndBuild(ctx, p.Ref); err != nil {
			return err
		}
	}
	id, err := r.start(ctx)
	if err != nil {
		return err
	}
	if err := r.healthGate(ctx, id); err != nil {
		return err
	}
	return r.activate(ctx)
}

// fetchAndBuild creates the deployment once the commit is known, then builds
// it. A resumed deployment keeps its commit, even if the branch moved.
func (r *deployRun) fetchAndBuild(ctx context.Context, ref string) error {
	if r.dep.ID != "" {
		ref = r.dep.SourceCommitSHA
	}
	if err := r.phase(ctx, PhaseFetch); err != nil {
		return err
	}
	defer func() {
		if err := r.Source.Cleanup(r.op.ID); err != nil {
			r.log.Warn("workspace cleanup failed", slog.Any("err", err))
		}
	}()
	r.event(ctx, store.LevelInfo, "fetching %s (branch %s)", r.app.RepoFullName, r.app.Branch)
	src, err := r.Source.Fetch(ctx, FetchRequest{OperationID: r.op.ID, Repo: r.app.RepoFullName, Branch: r.app.Branch,
		Ref: ref, Dockerfile: r.app.DockerfilePath, Context: r.app.BuildContext})
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if r.dep.ID == "" {
		// The environment is pinned now; later env changes need a new deploy.
		n := store.NewDeployment{OperationID: r.op.ID, SourceCommitSHA: src.SHA}
		rev, err := r.Store.LatestEnvRevision(ctx, r.app.ID)
		switch {
		case err == nil:
			n.EnvRevisionID = &rev.ID
		case !errors.Is(err, store.ErrNotFound):
			return fmt.Errorf("load environment: %w", err)
		}
		if r.dep, err = r.Store.CreateDeployment(ctx, r.owner, n); err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}
		r.log = r.log.With(slog.String("deployment_id", r.dep.ID))
	}
	r.event(ctx, store.LevelInfo, "deployment %s: commit %s", r.dep.ID, src.SHA)

	if err := r.phase(ctx, PhaseBuild); err != nil {
		return err
	}
	img, err := r.Builder.Build(ctx, BuildRequest{Dockerfile: src.Dockerfile, Context: src.Context,
		App: r.app.Slug, Commit: src.SHA, DeploymentID: r.dep.ID,
		Log: func(line string) { r.event(ctx, store.LevelInfo, "%s", line) }})
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}
	if err := r.Store.RecordImage(ctx, r.dep.ID, r.owner, img.ID, img.Metadata); err != nil {
		return fmt.Errorf("record image: %w", err)
	}
	r.dep.ImageID, r.dep.Status = img.ID, store.DeployStarting
	r.event(ctx, store.LevelInfo, "built image %s", img.ID)
	return nil
}

// start creates the candidate, persists its ID, then starts it.
func (r *deployRun) start(ctx context.Context) (string, error) {
	if err := r.phase(ctx, PhaseStart); err != nil {
		return "", err
	}
	env := map[string]string{}
	if r.dep.EnvRevisionID != nil {
		var err error
		if env, err = r.Env.Resolve(ctx, *r.dep.EnvRevisionID); err != nil {
			return "", fmt.Errorf("resolve environment: %w", err)
		}
	}
	id, err := r.Runtime.Create(ctx, Container{App: r.app.Slug, DeploymentID: r.dep.ID, Commit: r.dep.SourceCommitSHA,
		Image: r.dep.ImageID, Env: env, CPUs: r.app.CPULimit, Memory: r.app.MemoryLimit, StopTimeout: r.app.StopTimeout})
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	if id != r.dep.ContainerID {
		if err := r.Store.RecordContainer(ctx, r.dep.ID, r.owner, id); err != nil {
			return "", fmt.Errorf("record container: %w", err)
		}
		r.dep.ContainerID = id
	}
	if err := r.Runtime.Start(ctx, id); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}
	if err := r.Store.MarkHealthChecking(ctx, r.dep.ID, r.owner); err != nil {
		return "", fmt.Errorf("record start: %w", err)
	}
	return id, nil
}

// healthGate probes the candidate by IP (ARCHITECTURE §5 step 6). The
// container must keep running without restarts.
func (r *deployRun) healthGate(ctx context.Context, id string) error {
	if err := r.phase(ctx, PhaseHealth); err != nil {
		return err
	}
	alive := func(ctx context.Context) error {
		st, err := r.Runtime.Inspect(ctx, id)
		switch {
		case err != nil:
			return fmt.Errorf("inspect candidate: %w", err)
		case st.RestartCount > 0 || st.Restarting:
			return fmt.Errorf("container restarted (last exit code %d)", st.ExitCode)
		case !st.Running && st.OOMKilled:
			return errors.New("container was killed: out of memory")
		case !st.Running:
			return fmt.Errorf("container exited with code %d", st.ExitCode)
		case !st.IP.IsValid():
			return errors.New("container has no IP on its app network")
		}
		return nil
	}
	if err := alive(ctx); err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	st, err := r.Runtime.Inspect(ctx, id)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	url := "http://" + netip.AddrPortFrom(st.IP, uint16(r.app.InternalPort)).String() + r.app.HealthPath
	r.event(ctx, store.LevelInfo, "health check: GET %s (timeout %s)", r.app.HealthPath, r.app.HealthTimeout)
	if err := r.Health(ctx, url, r.app.HealthTimeout, alive); err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	return nil
}

// activate commits the new active deployment, then drains the one it
// replaced. With no route yet (Phase 2), nothing is served from the old one,
// so it stops right away instead of after an observation window.
func (r *deployRun) activate(ctx context.Context) error {
	if err := r.phase(ctx, PhaseActivate); err != nil {
		return err
	}
	prev, err := r.Store.ActivateDeployment(ctx, r.dep.ID, r.owner)
	if err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	r.event(ctx, store.LevelInfo, "deployment %s is active (commit %s)", r.dep.ID, r.dep.SourceCommitSHA)
	if prev != nil && prev.ContainerID != "" {
		err := r.Runtime.Stop(ctx, prev.ContainerID, r.app.StopTimeout)
		if err == nil {
			err = r.Runtime.Remove(ctx, prev.ContainerID)
		}
		if err != nil {
			// The deploy succeeded; the reconciler removes leftovers later.
			r.log.Warn("could not remove superseded deployment", slog.String("superseded_id", prev.ID), slog.Any("err", err))
			r.event(ctx, store.LevelWarn, "could not remove superseded deployment %s: %v", prev.ID, err)
		}
	}
	return nil
}

// fail records a failure: the candidate's last output, the candidate
// removed, the deployment and the operation failed. Nothing here touches
// the active deployment (invariant 5).
func (r *deployRun) fail(ctx context.Context, cause error) error {
	reason := cause.Error()
	r.log.Warn("deploy failed", slog.String("reason", firstLine(reason)))
	r.event(ctx, store.LevelError, "deploy failed: %s", reason)
	if r.dep.ContainerID != "" && r.dep.Status != store.DeployFailed {
		if lines, err := r.Runtime.Logs(ctx, r.dep.ContainerID, logTail); err == nil && len(lines) > 0 {
			r.event(ctx, store.LevelWarn, "last %d lines of the candidate's output:\n%s", len(lines), strings.Join(lines, "\n"))
		}
		if err := r.Runtime.Remove(ctx, r.dep.ContainerID); err != nil {
			r.log.Warn("could not remove the candidate", slog.String("container_id", r.dep.ContainerID), slog.Any("err", err))
			r.event(ctx, store.LevelWarn, "could not remove the candidate: %v", err)
		}
	}
	if r.dep.ID != "" && r.dep.Status != store.DeployFailed {
		if err := r.Store.FailDeployment(ctx, r.dep.ID, r.owner, reason); err != nil {
			return fmt.Errorf("record failure: %w", err)
		}
	}
	// A deploy failure is final: retrying the same commit and image rarely
	// helps, and the operator can deploy again.
	if _, err := r.Store.FailOperation(ctx, r.op.ID, r.owner, reason, 0); err != nil {
		return fmt.Errorf("record failure: %w", err)
	}
	return nil
}

func (r *deployRun) phase(ctx context.Context, phase string) error {
	if err := r.Store.SetOperationPhase(ctx, r.op.ID, r.owner, phase); err != nil {
		return fmt.Errorf("phase %s: %w", phase, err)
	}
	return nil
}

// event appends to the operation's log. A failed append is logged, never
// fatal: events are for humans, the phases carry the state.
func (r *deployRun) event(ctx context.Context, level, format string, args ...any) {
	if _, err := r.Store.AppendOperationEvent(ctx, r.op.ID, level, fmt.Sprintf(format, args...)); err != nil && ctx.Err() == nil {
		r.log.Warn("append operation event failed", slog.Any("err", err))
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
