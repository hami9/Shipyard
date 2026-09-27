package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

const (
	sha      = "abababababababababababababababababababab"
	image    = "sha256:0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f"
	opID     = "22222222-2222-4222-8222-222222222222"
	revID    = "33333333-3333-4333-8333-333333333333"
	owner    = "w1"
	oldCtrID = "old-container"
)

// fakeStore keeps one app, one operation, and its deployment in memory.
type fakeStore struct {
	app      store.App
	rev      *store.EnvRevision
	dep      *store.Deployment
	phases   []string
	events   []string
	opStatus string
	opReason string
	prev     *store.Deployment
	lostAt   string // a phase at which the lease is lost
}

func (s *fakeStore) AppByID(context.Context, string) (store.App, error) { return s.app, nil }
func (s *fakeStore) LatestEnvRevision(context.Context, string) (store.EnvRevision, error) {
	if s.rev == nil {
		return store.EnvRevision{}, store.ErrNotFound
	}
	return *s.rev, nil
}
func (s *fakeStore) SetOperationPhase(_ context.Context, _, _, phase string) error {
	if phase == s.lostAt {
		return store.ErrLeaseLost
	}
	s.phases = append(s.phases, phase)
	return nil
}
func (s *fakeStore) FailOperation(_ context.Context, _, _, reason string, _ time.Duration) (string, error) {
	s.opStatus, s.opReason = store.OpFailed, reason
	return store.OpFailed, nil
}
func (s *fakeStore) AppendOperationEvent(_ context.Context, _, level, msg string) (store.OperationEvent, error) {
	s.events = append(s.events, level+": "+msg)
	return store.OperationEvent{}, nil
}
func (s *fakeStore) CreateDeployment(_ context.Context, _ string, n store.NewDeployment) (store.Deployment, error) {
	s.dep = &store.Deployment{ID: "dep-1", OperationID: n.OperationID, SourceCommitSHA: n.SourceCommitSHA,
		EnvRevisionID: n.EnvRevisionID, Status: store.DeployBuilding}
	return *s.dep, nil
}
func (s *fakeStore) DeploymentByOperation(context.Context, string) (store.Deployment, error) {
	if s.dep == nil {
		return store.Deployment{}, store.ErrNotFound
	}
	return *s.dep, nil
}
func (s *fakeStore) RecordImage(_ context.Context, _, _, id string, _ json.RawMessage) error {
	s.dep.ImageID, s.dep.Status = id, store.DeployStarting
	return nil
}
func (s *fakeStore) RecordContainer(_ context.Context, _, _, id string) error {
	s.dep.ContainerID = id
	return nil
}
func (s *fakeStore) MarkHealthChecking(context.Context, string, string) error {
	s.dep.Status = store.DeployHealthChecking
	return nil
}
func (s *fakeStore) FailDeployment(_ context.Context, _, _, reason string) error {
	s.dep.Status, s.dep.FailureReason = store.DeployFailed, reason
	return nil
}
func (s *fakeStore) ActivateDeployment(context.Context, string, string) (*store.Deployment, error) {
	s.dep.Status, s.opStatus = store.DeployActive, store.OpSucceeded
	return s.prev, nil
}

type fakeSource struct {
	req      FetchRequest
	fetched  int
	cleaned  int
	fetchErr error
}

func (f *fakeSource) Fetch(_ context.Context, r FetchRequest) (Fetched, error) {
	f.req, f.fetched = r, f.fetched+1
	if f.fetchErr != nil {
		return Fetched{}, f.fetchErr
	}
	ref := r.Ref
	if ref == "" {
		ref = sha
	}
	return Fetched{SHA: ref, Dockerfile: "/w/Dockerfile", Context: "/w"}, nil
}
func (f *fakeSource) Cleanup(string) error { f.cleaned++; return nil }

type fakeBuilder struct {
	req   BuildRequest
	built int
	err   error
}

func (b *fakeBuilder) Build(_ context.Context, r BuildRequest) (Image, error) {
	b.req, b.built = r, b.built+1
	r.Log("#1 building")
	if b.err != nil {
		return Image{}, b.err
	}
	return Image{ID: image}, nil
}

type fakeRuntime struct {
	created []Container
	calls   []string
	state   ContainerState
}

func (f *fakeRuntime) Create(_ context.Context, c Container) (string, error) {
	f.created = append(f.created, c)
	return "ctr-" + c.DeploymentID, nil
}
func (f *fakeRuntime) Start(_ context.Context, id string) error {
	f.calls = append(f.calls, "start "+id)
	return nil
}
func (f *fakeRuntime) Inspect(context.Context, string) (ContainerState, error) {
	return f.state, nil
}
func (f *fakeRuntime) Stop(_ context.Context, id string, d time.Duration) error {
	f.calls = append(f.calls, "stop "+id+" "+d.String())
	return nil
}
func (f *fakeRuntime) Remove(_ context.Context, id string) error {
	f.calls = append(f.calls, "remove "+id)
	return nil
}
func (f *fakeRuntime) Logs(context.Context, string, int) ([]string, error) {
	return []string{"listening on :3000", "panic: boom"}, nil
}

type fakeEnv map[string]string

func (e fakeEnv) Resolve(_ context.Context, id string) (map[string]string, error) {
	if id != revID {
		return nil, errors.New("unknown revision")
	}
	return e, nil
}

type harness struct {
	st     *fakeStore
	src    *fakeSource
	bld    *fakeBuilder
	rt     *fakeRuntime
	d      *Deployer
	probed string
	health error
}

func newHarness() *harness {
	h := &harness{
		st: &fakeStore{app: store.App{ID: "app-1", Slug: "web", RepoFullName: "hami9/demo", Branch: "main",
			DockerfilePath: "Dockerfile", BuildContext: ".", InternalPort: 3000, HealthPath: "/healthz",
			HealthTimeout: time.Minute, CPULimit: 0.5, MemoryLimit: 64 << 20, StopTimeout: 7 * time.Second}},
		src: &fakeSource{},
		bld: &fakeBuilder{},
		rt:  &fakeRuntime{state: ContainerState{Running: true, IP: netip.MustParseAddr("172.20.0.5")}},
	}
	h.d = &Deployer{Store: h.st, Source: h.src, Builder: h.bld, Runtime: h.rt, Env: fakeEnv{"API_KEY": "s3cret"},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: func(ctx context.Context, url string, _ time.Duration, alive func(context.Context) error) error {
			h.probed = url
			if err := alive(ctx); err != nil {
				return err
			}
			return h.health
		}}
	return h
}

func (h *harness) run(t *testing.T, payload string) error {
	t.Helper()
	return h.d.Run(t.Context(), store.Operation{ID: opID, AppID: "app-1", Kind: "deploy", Payload: json.RawMessage(payload)}, owner)
}

func (h *harness) logged(s string) bool {
	return slices.ContainsFunc(h.st.events, func(e string) bool { return strings.Contains(e, s) })
}

func TestDeployHappyPath(t *testing.T) {
	h := newHarness()
	h.st.rev = &store.EnvRevision{ID: revID}
	h.st.prev = &store.Deployment{ID: "dep-0", ContainerID: oldCtrID}
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	if want := []string{PhaseFetch, PhaseBuild, PhaseStart, PhaseHealth, PhaseActivate}; !slices.Equal(h.st.phases, want) {
		t.Errorf("phases = %v, want %v", h.st.phases, want)
	}
	if h.st.dep.Status != store.DeployActive || h.st.opStatus != store.OpSucceeded || h.st.dep.SourceCommitSHA != sha {
		t.Errorf("deployment = %+v, op = %s", h.st.dep, h.st.opStatus)
	}
	if h.st.dep.EnvRevisionID == nil || *h.st.dep.EnvRevisionID != revID {
		t.Errorf("env revision not pinned: %v", h.st.dep.EnvRevisionID)
	}
	if h.src.req != (FetchRequest{OperationID: opID, Repo: "hami9/demo", Branch: "main", Dockerfile: "Dockerfile", Context: "."}) || h.src.cleaned != 1 {
		t.Errorf("fetch = %+v, cleaned %d", h.src.req, h.src.cleaned)
	}
	if h.bld.req.Dockerfile != "/w/Dockerfile" || h.bld.req.App != "web" || h.bld.req.Commit != sha || h.bld.req.DeploymentID != "dep-1" {
		t.Errorf("build = %+v", h.bld.req)
	}
	c := h.rt.created[0]
	if c.Image != image || c.Env["API_KEY"] != "s3cret" || c.CPUs != 0.5 || c.Memory != 64<<20 || c.StopTimeout != 7*time.Second {
		t.Errorf("container = %+v", c)
	}
	if h.st.dep.ContainerID != "ctr-dep-1" || h.probed != "http://172.20.0.5:3000/healthz" {
		t.Errorf("container id %q, probed %q", h.st.dep.ContainerID, h.probed)
	}
	// The superseded container is drained with the app's stop timeout.
	if want := []string{"start ctr-dep-1", "stop old-container 7s", "remove old-container"}; !slices.Equal(h.rt.calls, want) {
		t.Errorf("runtime calls = %v, want %v", h.rt.calls, want)
	}
	if !h.logged("#1 building") || !h.logged("is active") {
		t.Errorf("events = %v", h.st.events)
	}
	// Invariant 8: secret values never reach the operation log.
	if h.logged("s3cret") {
		t.Error("a secret value was logged")
	}
}

func TestDeployPinnedRef(t *testing.T) {
	h := newHarness()
	ref := strings.Repeat("cd", 20)
	if err := h.run(t, `{"ref":"`+ref+`"}`); err != nil {
		t.Fatal(err)
	}
	if h.src.req.Ref != ref || h.st.dep.SourceCommitSHA != ref {
		t.Fatalf("ref %q, deployment commit %q", h.src.req.Ref, h.st.dep.SourceCommitSHA)
	}
}

func TestDeployFetchFailure(t *testing.T) {
	h := newHarness()
	h.src.fetchErr = errors.New("commit is not on the tracked branch")
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	// No deployment exists for a commit that was never verified.
	if h.st.dep != nil || h.st.opStatus != store.OpFailed || !strings.Contains(h.st.opReason, "not on the tracked branch") {
		t.Fatalf("dep = %+v, op = %s %q", h.st.dep, h.st.opStatus, h.st.opReason)
	}
}

func TestDeployBuildFailure(t *testing.T) {
	h := newHarness()
	h.bld.err = errors.New("image build failed: COPY missing-file")
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	if h.st.dep.Status != store.DeployFailed || !strings.Contains(h.st.dep.FailureReason, "missing-file") ||
		h.st.opStatus != store.OpFailed || len(h.rt.created) != 0 || h.src.cleaned != 1 {
		t.Fatalf("dep = %+v, op = %s, created %d, cleaned %d", h.st.dep, h.st.opStatus, len(h.rt.created), h.src.cleaned)
	}
}

// Invariant 5: an unhealthy candidate is removed, with its last output kept,
// and the active deployment is never touched.
func TestDeployUnhealthy(t *testing.T) {
	h := newHarness()
	h.st.prev = &store.Deployment{ID: "dep-0", ContainerID: oldCtrID}
	h.health = errors.New("health check did not pass within 1m0s: GET /healthz: status 500")
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	if h.st.dep.Status != store.DeployFailed || h.st.opStatus != store.OpFailed || !strings.Contains(h.st.opReason, "status 500") {
		t.Fatalf("dep = %+v, op = %s %q", h.st.dep, h.st.opStatus, h.st.opReason)
	}
	if want := []string{"start ctr-dep-1", "remove ctr-dep-1"}; !slices.Equal(h.rt.calls, want) {
		t.Errorf("runtime calls = %v, want %v (old container must stay)", h.rt.calls, want)
	}
	if !h.logged("panic: boom") {
		t.Errorf("candidate output not captured: %v", h.st.events)
	}
}

func TestDeployContainerExits(t *testing.T) {
	for name, tc := range map[string]struct {
		state ContainerState
		want  string
	}{
		"exited":    {ContainerState{ExitCode: 3, IP: netip.MustParseAddr("172.20.0.5")}, "exited with code 3"},
		"oom":       {ContainerState{OOMKilled: true, ExitCode: 137}, "out of memory"},
		"restarted": {ContainerState{Running: true, RestartCount: 1, ExitCode: 1}, "restarted"},
		"no ip":     {ContainerState{Running: true}, "no IP"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			h.rt.state = tc.state
			if err := h.run(t, `{}`); err != nil {
				t.Fatal(err)
			}
			if h.st.opStatus != store.OpFailed || !strings.Contains(h.st.opReason, tc.want) {
				t.Fatalf("op = %s %q, want %q", h.st.opStatus, h.st.opReason, tc.want)
			}
		})
	}
}

// A retried operation resumes its deployment: the recorded image is reused,
// nothing is fetched or rebuilt, and the commit cannot move.
func TestDeployResumesAfterBuild(t *testing.T) {
	h := newHarness()
	rev := revID
	h.st.dep = &store.Deployment{ID: "dep-1", OperationID: opID, SourceCommitSHA: sha, ImageID: image,
		EnvRevisionID: &rev, ContainerID: "ctr-dep-1", Status: store.DeployHealthChecking}
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	if h.src.fetched != 0 || h.bld.built != 0 {
		t.Fatalf("fetched %d, built %d; want neither", h.src.fetched, h.bld.built)
	}
	if want := []string{PhaseStart, PhaseHealth, PhaseActivate}; !slices.Equal(h.st.phases, want) {
		t.Errorf("phases = %v", h.st.phases)
	}
	if h.rt.created[0].Image != image || h.st.dep.Status != store.DeployActive || !h.logged("resuming deployment dep-1") {
		t.Errorf("created %+v, dep %+v", h.rt.created, h.st.dep)
	}
}

// A deployment created before a crash keeps its commit on the rebuild.
func TestDeployResumesBeforeBuild(t *testing.T) {
	h := newHarness()
	pinned := strings.Repeat("ef", 20)
	h.st.dep = &store.Deployment{ID: "dep-1", OperationID: opID, SourceCommitSHA: pinned, Status: store.DeployBuilding}
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	if h.src.req.Ref != pinned || h.bld.req.Commit != pinned {
		t.Fatalf("fetch ref %q, build commit %q; want %s", h.src.req.Ref, h.bld.req.Commit, pinned)
	}
}

// A lost lease stops the run without recording anything: the new owner
// resumes it.
func TestDeployLeaseLost(t *testing.T) {
	h := newHarness()
	h.st.lostAt = PhaseHealth
	err := h.run(t, `{}`)
	if !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("err = %v", err)
	}
	if h.st.opStatus != "" || h.st.dep.Status == store.DeployFailed || slices.Contains(h.rt.calls, "remove ctr-dep-1") {
		t.Fatalf("recorded after losing the lease: op %q, dep %+v, calls %v", h.st.opStatus, h.st.dep, h.rt.calls)
	}
}

// A deployment already marked failed only needs its operation closed.
func TestDeployResumesFailed(t *testing.T) {
	h := newHarness()
	h.st.dep = &store.Deployment{ID: "dep-1", Status: store.DeployFailed, FailureReason: "build: boom", ContainerID: "ctr-dep-1"}
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	if h.st.opStatus != store.OpFailed || h.st.opReason != "build: boom" || len(h.rt.calls) != 0 || h.src.fetched != 0 {
		t.Fatalf("op %s %q, runtime calls %v", h.st.opStatus, h.st.opReason, h.rt.calls)
	}
}

func TestDeployBadPayload(t *testing.T) {
	h := newHarness()
	if err := h.run(t, `[1]`); err != nil {
		t.Fatal(err)
	}
	if h.st.opStatus != store.OpFailed || !strings.Contains(h.st.opReason, "invalid deploy payload") {
		t.Fatalf("op %s %q", h.st.opStatus, h.st.opReason)
	}
}
