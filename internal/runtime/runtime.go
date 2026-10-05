// Package runtime runs app containers on Docker Engine (ARCHITECTURE §5 step
// 5, §7): one user-defined bridge network per app, and containers created from
// an Engine image ID with the hardened flag set. Names and io.shipyard.*
// labels are deterministic, so every call is safe to repeat after a crash
// (invariant 3).
package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// DefaultPidsLimit applies when a Spec leaves PidsLimit at zero. Apps have no
// per-app pids setting yet; 512 leaves room for worker pools while stopping a
// fork bomb.
const DefaultPidsLimit = 512

// Docker's minimum memory limit [DK-RESOURCES], as in the apps table.
const minMemory = 6 << 20

const (
	labelManaged    = "io.shipyard.managed"
	labelApp        = "io.shipyard.app"
	labelDeployment = "io.shipyard.deployment"
	labelCommit     = "io.shipyard.commit"
)

var (
	// ErrNotFound: the container, image, or network does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNotManaged: a Docker object with that name or ID exists but carries
	// no Shipyard labels for this app. Shipyard never adopts or touches it.
	ErrNotManaged = errors.New("not managed by shipyard")
	// ErrNameTaken: the deterministic container name belongs to a container
	// with a different deployment or image.
	ErrNameTaken = errors.New("container name taken by a different container")
	ErrInvalid   = errors.New("invalid container spec")
)

// AllowedCaps are the only capabilities an app may add back after
// --cap-drop ALL (ARCHITECTURE §7). All are in Docker's default set [DK-SEC];
// none grants administration of the host, its network, or other processes.
var AllowedCaps = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "NET_BIND_SERVICE", "SETGID", "SETUID"}

var (
	slugRE    = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$`)
	shaRE     = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	uuidRE    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	imageIDRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	envKeyRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,254}$`)
)

// NetworkName is the per-app bridge network.
func NetworkName(app string) string { return "shipyard-app-" + app }

// ContainerName is the deterministic name of a deployment's container.
func ContainerName(app, deploymentID string) string { return "shipyard-" + app + "-" + deploymentID }

// Spec is one deployment's container. Only the fields below reach Docker, so
// the forbidden options (privileged, host network, mounts, published ports)
// cannot be requested at all.
type Spec struct {
	App          string // slug
	DeploymentID string
	Commit       string
	Image        string            // Engine image ID (ADR-0004), never a tag
	Env          map[string]string // the resolved revision (secrets.Env.Resolve)
	CPUs         float64           // --cpus
	Memory       int64             // --memory, bytes
	PidsLimit    int64             // --pids-limit; DefaultPidsLimit when zero
	StopTimeout  time.Duration     // SIGTERM → SIGKILL grace, whole seconds
	CapAdd       []string          // a subset of AllowedCaps
	ReadOnly     bool              // opt-in --read-only
	Init         bool              // opt-in --init
}

// Validate reports the first invalid field. Messages name env keys, never
// values (invariant 8).
func (s Spec) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	switch {
	case !slugRE.MatchString(s.App):
		return bad("app slug %q", s.App)
	case !uuidRE.MatchString(s.DeploymentID):
		return bad("deployment id %q", s.DeploymentID)
	case !shaRE.MatchString(s.Commit):
		return bad("commit %q", s.Commit)
	case !imageIDRE.MatchString(s.Image):
		return bad("image must be an Engine image ID (sha256:…), got %q", s.Image)
	case !(s.CPUs > 0) || math.IsInf(s.CPUs, 0):
		return bad("cpus must be positive")
	case s.Memory < minMemory:
		return bad("memory must be at least %d bytes", minMemory)
	case s.PidsLimit < 0:
		return bad("pids limit must not be negative")
	case s.StopTimeout < 0 || s.StopTimeout > 10*time.Minute:
		return bad("stop timeout must be between 0 and 10m")
	}
	for k, v := range s.Env {
		if !envKeyRE.MatchString(k) {
			return bad("env key %q", k)
		}
		if strings.IndexByte(v, 0) >= 0 {
			return bad("env %s: value contains NUL", k)
		}
	}
	for _, c := range s.CapAdd {
		if !slices.Contains(AllowedCaps, c) {
			return bad("capability %q is not in the allowlist", c)
		}
	}
	return nil
}

// createOptions maps a valid Spec to the Engine request. It is the single
// place the hardened flag set is defined (invariant 10, ARCHITECTURE §7).
func (s Spec) createOptions() client.ContainerCreateOptions {
	netName := NetworkName(s.App)
	pids := s.PidsLimit
	if pids == 0 {
		pids = DefaultPidsLimit
	}
	stop := int(math.Ceil(s.StopTimeout.Seconds()))
	initProc := s.Init
	env := make([]string, 0, len(s.Env))
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		env = append(env, k+"="+s.Env[k])
	}
	return client.ContainerCreateOptions{
		Name: ContainerName(s.App, s.DeploymentID),
		Config: &container.Config{
			Image:       s.Image,
			Env:         env,
			Labels:      s.labels(),
			StopTimeout: &stop,
		},
		HostConfig: &container.HostConfig{
			NetworkMode:   container.NetworkMode(netName),
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			// Set per container: the daemon default may still be json-file,
			// which never rotates [DK-LOG][DK-LOG-LOCAL].
			LogConfig:      container.LogConfig{Type: "local"},
			CapDrop:        []string{"ALL"},
			CapAdd:         slices.Clone(s.CapAdd),
			SecurityOpt:    []string{"no-new-privileges"},
			ReadonlyRootfs: s.ReadOnly,
			Init:           &initProc,
			Resources: container.Resources{
				Memory:    s.Memory,
				NanoCPUs:  int64(math.Round(s.CPUs * 1e9)),
				PidsLimit: &pids,
			},
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{netName: {}},
		},
	}
}

func (s Spec) labels() map[string]string {
	return map[string]string{
		labelManaged:    "true",
		labelApp:        s.App,
		labelDeployment: s.DeploymentID,
		labelCommit:     s.Commit,
	}
}

// State is what the worker needs from a container: whether it runs, whether
// it keeps restarting, and where to probe it.
type State struct {
	ID           string
	Name         string // resolvable on the app network (Docker's embedded DNS) [DK-BRIDGE]
	App          string
	DeploymentID string
	Status       string // created, running, restarting, exited, …
	Running      bool
	Restarting   bool
	OOMKilled    bool
	ExitCode     int
	RestartCount int
	IP           netip.Addr // on the app network; invalid when not attached
}

// Runtime talks to Docker Engine. Only the worker uses it (invariant 1).
type Runtime struct {
	cli *client.Client
	// Edge names the Caddy container (EnsureEdge). When set, every app
	// network that EnsureNetwork or Create makes is joined by it (ADR-0003).
	Edge string
}

// New connects using DOCKER_HOST and friends, or the default socket.
// The API version is negotiated on the first request.
func New() (*Runtime, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Runtime{cli: cli}, nil
}

func (r *Runtime) Close() error { return r.cli.Close() }

// DockerRootDir is the Engine's data root, where images, the build cache,
// container logs and volumes live (P5.6 watches its disk).
func (r *Runtime) DockerRootDir(ctx context.Context) (string, error) {
	got, err := r.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", fmt.Errorf("docker info: %w", wrap(err))
	}
	if got.Info.DockerRootDir == "" {
		return "", errors.New("docker info reports no data root")
	}
	return got.Info.DockerRootDir, nil
}

// EnsureNetwork creates the app's bridge network if it is missing and returns
// its ID. An existing network with that name is used only if Shipyard created
// it for this app.
func (r *Runtime) EnsureNetwork(ctx context.Context, app string) (string, error) {
	if !slugRE.MatchString(app) {
		return "", fmt.Errorf("%w: app slug %q", ErrInvalid, app)
	}
	id, err := r.ensureBridge(ctx, NetworkName(app), map[string]string{labelManaged: "true", labelApp: app})
	if err != nil {
		return "", err
	}
	if r.Edge != "" {
		if err := r.attachEdge(ctx, r.Edge, NetworkName(app)); err != nil {
			return "", err
		}
	}
	return id, nil
}

// ensureBridge creates a bridge network with these labels if it is missing.
// An existing network is used only if it carries all of them.
func (r *Runtime) ensureBridge(ctx context.Context, name string, labels map[string]string) (string, error) {
	for range 2 {
		got, err := r.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
		if err == nil {
			n := got.Network
			if n.Name != name || n.Driver != "bridge" || !hasLabels(n.Labels, labels) {
				return "", fmt.Errorf("network %s: %w", name, ErrNotManaged)
			}
			return n.ID, nil
		}
		if !cerrdefs.IsNotFound(err) {
			return "", fmt.Errorf("inspect network %s: %w", name, err)
		}
		created, err := r.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge", Labels: labels})
		if err == nil {
			return created.ID, nil
		}
		if !cerrdefs.IsConflict(err) {
			return "", fmt.Errorf("create network %s: %w", name, err)
		}
		// Another caller created it first; inspect it again.
	}
	return "", fmt.Errorf("network %s: created and deleted concurrently", name)
}

// RemoveNetwork deletes the app's network. A missing network is not an error.
func (r *Runtime) RemoveNetwork(ctx context.Context, app string) error {
	name := NetworkName(app)
	got, err := r.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect network %s: %w", name, err)
	}
	if !ownedBy(got.Network.Labels, app) {
		return fmt.Errorf("network %s: %w", name, ErrNotManaged)
	}
	if r.Edge != "" { // a network with a connected edge cannot be removed
		_, err := r.cli.NetworkDisconnect(ctx, got.Network.ID, client.NetworkDisconnectOptions{Container: r.Edge, Force: true})
		if err != nil && !cerrdefs.IsNotFound(err) && !strings.Contains(err.Error(), "is not connected") {
			return fmt.Errorf("detach edge from %s: %w", name, err)
		}
	}
	if _, err := r.cli.NetworkRemove(ctx, got.Network.ID, client.NetworkRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove network %s: %w", name, err)
	}
	return nil
}

// Create creates the deployment's container without starting it and returns
// its ID; the caller persists the ID before Start (ARCHITECTURE §5 step 5).
// Repeating Create for the same deployment and image returns the same
// container.
func (r *Runtime) Create(ctx context.Context, s Spec) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	// Engine 29 accepts a missing network at create and fails only at start
	// (observed, see WORKLOG P1.10), so make sure ours exists first. This
	// also refuses a foreign network that holds the name.
	if _, err := r.EnsureNetwork(ctx, s.App); err != nil {
		return "", err
	}
	opts := s.createOptions()
	res, err := r.cli.ContainerCreate(ctx, opts)
	if err == nil {
		return res.ID, nil
	}
	if !cerrdefs.IsConflict(err) {
		return "", fmt.Errorf("create container %s: %w", opts.Name, wrap(err))
	}
	// The name exists, most likely from an attempt that crashed after Create.
	got, err := r.cli.ContainerInspect(ctx, opts.Name, client.ContainerInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w", opts.Name, wrap(err))
	}
	c := got.Container
	if c.Config == nil || !ownedBy(c.Config.Labels, s.App) ||
		c.Config.Labels[labelDeployment] != s.DeploymentID || c.Image != s.Image {
		return "", fmt.Errorf("container %s: %w", opts.Name, ErrNameTaken)
	}
	return c.ID, nil
}

// Start starts a managed container. Starting a running container succeeds.
func (r *Runtime) Start(ctx context.Context, id string) error {
	if _, err := r.managed(ctx, id); err != nil {
		return err
	}
	if _, err := r.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start container %s: %w", id, wrap(err))
	}
	return nil
}

// Inspect returns the state of a managed container.
func (r *Runtime) Inspect(ctx context.Context, id string) (State, error) {
	c, err := r.managed(ctx, id)
	if err != nil {
		return State{}, err
	}
	st := State{
		ID:           c.ID,
		Name:         strings.TrimPrefix(c.Name, "/"),
		App:          c.Config.Labels[labelApp],
		DeploymentID: c.Config.Labels[labelDeployment],
		RestartCount: c.RestartCount,
	}
	if c.State != nil {
		st.Status = string(c.State.Status)
		st.Running = c.State.Running
		st.Restarting = c.State.Restarting
		st.OOMKilled = c.State.OOMKilled
		st.ExitCode = c.State.ExitCode
	}
	if c.NetworkSettings != nil {
		if ep := c.NetworkSettings.Networks[NetworkName(st.App)]; ep != nil {
			st.IP = ep.IPAddress
		}
	}
	return st, nil
}

// Stop sends SIGTERM, then SIGKILL after timeout (whole seconds, rounded up)
// [DK-RUN]. A stopped or missing container is not an error.
func (r *Runtime) Stop(ctx context.Context, id string, timeout time.Duration) error {
	if _, err := r.managed(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	secs := int(math.Ceil(max(timeout, 0).Seconds()))
	if _, err := r.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &secs}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("stop container %s: %w", id, wrap(err))
	}
	return nil
}

// Remove force-removes a managed container and its anonymous volumes. A
// missing container is not an error. The image stays (retention, ADR-0006).
func (r *Runtime) Remove(ctx context.Context, id string) error {
	if _, err := r.managed(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	_, err := r.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove container %s: %w", id, wrap(err))
	}
	return nil
}

// ImageExists reports whether an image ID is still on the host, so a
// rollback knows whether it can run without rebuilding (invariant 6).
func (r *Runtime) ImageExists(ctx context.Context, id string) (bool, error) {
	if _, err := r.cli.ImageInspect(ctx, id); err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect image %s: %w", id, err)
	}
	return true, nil
}

// ErrInUse: Docker refuses to remove an image without force (409): a
// container still uses it, or it has more than one tag [DK-RMI].
var ErrInUse = errors.New("image in use")

// ListImages returns the IDs of the images Shipyard built (by label),
// tagged or not.
func (r *Runtime) ListImages(ctx context.Context) ([]string, error) {
	res, err := r.cli.ImageList(ctx, client.ImageListOptions{
		Filters: make(client.Filters).Add("label", labelManaged+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	var out []string
	for _, img := range res.Items {
		if imageIDRE.MatchString(img.ID) {
			out = append(out, img.ID)
		}
	}
	return out, nil
}

// RemoveImage deletes an image Shipyard built, with its tag, never forced:
// an image a container uses, running or stopped, or one an operator tagged
// again, stays and is ErrInUse. A missing image is not an error.
func (r *Runtime) RemoveImage(ctx context.Context, id string) error {
	if !imageIDRE.MatchString(id) {
		return fmt.Errorf("%w: image must be an Engine image ID, got %q", ErrInvalid, id)
	}
	got, err := r.cli.ImageInspect(ctx, id)
	switch {
	case cerrdefs.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("inspect image %s: %w", id, err)
	case got.Config == nil || got.Config.Labels[labelManaged] != "true":
		return fmt.Errorf("image %s: %w", id, ErrNotManaged)
	}
	// Ours have one tag, shipyard/<slug>:<sha12>, or none once a rebuild
	// moved it; a second tag is someone's intent to keep it [DK-RMI].
	_, err = r.cli.ImageRemove(ctx, id, client.ImageRemoveOptions{PruneChildren: true})
	switch {
	case err == nil, cerrdefs.IsNotFound(err):
		return nil
	case cerrdefs.IsConflict(err):
		return fmt.Errorf("remove image %s: %w: %w", id, ErrInUse, err)
	}
	return fmt.Errorf("remove image %s: %w", id, err)
}

// Managed is one of Shipyard's app containers, as the reconciler sees it.
type Managed struct {
	ID           string
	App          string
	DeploymentID string
	Running      bool
}

// ListManaged returns every app container Shipyard created (running or not),
// by label; the edge and foreign containers are not included.
func (r *Runtime) ListManaged(ctx context.Context) ([]Managed, error) {
	res, err := r.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", labelManaged+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	var out []Managed
	for _, c := range res.Items {
		app, dep := c.Labels[labelApp], c.Labels[labelDeployment]
		if !slugRE.MatchString(app) || !uuidRE.MatchString(dep) {
			continue
		}
		out = append(out, Managed{ID: c.ID, App: app, DeploymentID: dep, Running: c.State == container.StateRunning})
	}
	return out, nil
}

// maxLogBytes bounds what Logs reads, whatever the line lengths.
const maxLogBytes = 256 << 10

// Logs returns up to tail last lines of a managed container's stdout and
// stderr, e.g. to explain a failed health check. Output is the app's own.
func (r *Runtime) Logs(ctx context.Context, id string, tail int) ([]string, error) {
	if _, err := r.managed(ctx, id); err != nil {
		return nil, err
	}
	rc, err := r.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(max(tail, 1)),
	})
	if err != nil {
		return nil, fmt.Errorf("logs of container %s: %w", id, wrap(err))
	}
	defer rc.Close()
	// Containers never get a TTY, so the stream is multiplexed [MOBY-CLIENT].
	// A stream cut at the byte limit ends mid-frame; keep what was read.
	var buf bytes.Buffer
	if _, err := stdcopy.StdCopy(&buf, &buf, io.LimitReader(rc, maxLogBytes)); err != nil && buf.Len() == 0 {
		return nil, fmt.Errorf("logs of container %s: %w", id, err)
	}
	out := strings.TrimRight(buf.String(), "\n")
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// managed inspects a container and refuses anything Shipyard did not create,
// so a wrong ID can never stop or remove an unrelated container on the host.
func (r *Runtime) managed(ctx context.Context, id string) (container.InspectResponse, error) {
	if id == "" {
		return container.InspectResponse{}, fmt.Errorf("%w: empty container id", ErrInvalid)
	}
	got, err := r.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, fmt.Errorf("inspect container %s: %w", id, wrap(err))
	}
	c := got.Container
	if c.Config == nil || c.Config.Labels[labelManaged] != "true" || !slugRE.MatchString(c.Config.Labels[labelApp]) {
		return container.InspectResponse{}, fmt.Errorf("container %s: %w", id, ErrNotManaged)
	}
	return c, nil
}

func ownedBy(labels map[string]string, app string) bool {
	return labels[labelManaged] == "true" && labels[labelApp] == app
}

func hasLabels(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// wrap adds ErrNotFound to Engine 404s so callers need not import errdefs.
func wrap(err error) error {
	if cerrdefs.IsNotFound(err) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}
