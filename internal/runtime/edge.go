package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// The edge is the Caddy container (ADR-0003): the only container that
// publishes host ports (invariant 11), joined to every app network, with its
// admin API only on a permissioned Unix socket (invariant 12).

// DefaultEdgeImage is the official Caddy image, pinned to its multi-arch
// index digest [CADDY-IMAGE].
const DefaultEdgeImage = "caddy:2.11.4-alpine@sha256:6aeddd44c3078b0f9a35206472a11420648a79c184603ef95957d0a20044cb2b"

// AdminSocketName is the admin socket's file name inside EdgeSpec.AdminDir.
const AdminSocketName = "caddy-admin.sock"

const (
	labelRole = "io.shipyard.role"
	roleEdge  = "edge"
	// labelSpec holds a hash of the EdgeSpec, so a changed spec (image,
	// ports, socket directory) recreates the container.
	labelSpec = "io.shipyard.edge-spec"
)

// Limits of the edge container. Caddy on one VPS needs little.
const (
	edgeMemory = 512 << 20
	edgeCPUs   = 1
	edgePids   = 512
)

var edgeNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// EdgeSpec configures the Caddy container.
type EdgeSpec struct {
	// Name of the container, its network, and (with -data/-config) its
	// volumes. It must not look like an app network.
	Name  string
	Image string
	// AdminDir is a host directory, mounted at the same path, that holds the
	// admin socket. It is made group-owned by GID with mode 2770, and the
	// socket is 0660, so only that group (the worker's) can use it.
	AdminDir string
	GID      int
	// Host side of the published ports. Port 0 lets Docker choose (tests);
	// an invalid BindIP means all interfaces.
	BindIP              netip.Addr
	HTTPPort, HTTPSPort int
}

// AdminSocket is the host path of the admin socket.
func (s EdgeSpec) AdminSocket() string { return filepath.Join(s.AdminDir, AdminSocketName) }

// Validate reports the first invalid field.
func (s EdgeSpec) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: edge %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	switch {
	case !edgeNameRE.MatchString(s.Name) || strings.HasPrefix(s.Name, NetworkName("")):
		return bad("name %q", s.Name)
	case s.Image == "":
		return bad("image is empty")
	case !filepath.IsAbs(s.AdminDir) || filepath.Clean(s.AdminDir) != s.AdminDir || strings.ContainsAny(s.AdminDir, "|:,"):
		return bad("admin directory %q must be a clean absolute path", s.AdminDir)
	case s.GID <= 0:
		return bad("admin group %d must be a non-root group", s.GID)
	case s.HTTPPort < 0 || s.HTTPPort > 65535 || s.HTTPSPort < 0 || s.HTTPSPort > 65535:
		return bad("ports %d/%d", s.HTTPPort, s.HTTPSPort)
	}
	return nil
}

func (s EdgeSpec) hash() string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func (s EdgeSpec) labels() map[string]string {
	return map[string]string{labelManaged: "true", labelRole: roleEdge}
}

// createOptions is the single place the edge container is defined.
func (s EdgeSpec) createOptions() client.ContainerCreateOptions {
	port := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	bind := func(n int) []network.PortBinding { return []network.PortBinding{{HostIP: s.BindIP, HostPort: port(n)}} }
	labels := s.labels()
	labels[labelSpec] = s.hash()
	pids := int64(edgePids)
	return client.ContainerCreateOptions{
		Name: s.Name,
		Config: &container.Config{
			Image: s.Image,
			// --resume restarts from the last loaded (autosaved) config, so a
			// restart keeps serving before the worker reloads it [CADDY-CLI].
			Cmd: []string{"caddy", "run", "--resume"},
			// The default admin address; a loaded config may not move it
			// elsewhere (internal/routing always renders this socket) [CADDY-API].
			Env:    []string{"CADDY_ADMIN=unix/" + s.AdminSocket() + "|0660"},
			Labels: labels,
			// Root, so Caddy can bind 80/443 with only NET_BIND_SERVICE, but
			// with the worker's group, so the admin socket is created in a
			// directory it may write without CAP_DAC_OVERRIDE.
			User: "0:" + strconv.Itoa(s.GID),
			ExposedPorts: network.PortSet{
				network.MustParsePort("80/tcp"): {}, network.MustParsePort("443/tcp"): {}, network.MustParsePort("443/udp"): {},
			},
		},
		HostConfig: &container.HostConfig{
			NetworkMode: container.NetworkMode(s.Name),
			// The only published ports on the host (invariant 11). 443/udp
			// is HTTP/3.
			PortBindings: network.PortMap{
				network.MustParsePort("80/tcp"):  bind(s.HTTPPort),
				network.MustParsePort("443/tcp"): bind(s.HTTPSPort),
				network.MustParsePort("443/udp"): bind(s.HTTPSPort),
			},
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			LogConfig:      container.LogConfig{Type: "local"},
			CapDrop:        []string{"ALL"},
			CapAdd:         []string{"NET_BIND_SERVICE"},
			SecurityOpt:    []string{"no-new-privileges"},
			ReadonlyRootfs: true,
			Tmpfs:          map[string]string{"/tmp": "rw,noexec,nosuid,size=16m"},
			Resources:      container.Resources{Memory: edgeMemory, NanoCPUs: edgeCPUs * 1e9, PidsLimit: &pids},
			Mounts: []mount.Mount{
				// Certificates and ACME state must persist [CADDY-HTTPS]; the
				// autosaved config lives in /config.
				{Type: mount.TypeVolume, Source: s.Name + "-data", Target: "/data"},
				{Type: mount.TypeVolume, Source: s.Name + "-config", Target: "/config"},
				// The one host bind mount: the socket directory, nothing else.
				{Type: mount.TypeBind, Source: s.AdminDir, Target: s.AdminDir},
			},
		},
	}
}

// EnsureEdge makes the Caddy container exist, match spec, and run, joined to
// every app network, and waits until its admin socket accepts connections.
// It is idempotent; a changed spec recreates the container, keeping its
// volumes (certificates and the autosaved config).
func (r *Runtime) EnsureEdge(ctx context.Context, s EdgeSpec) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	if err := prepareAdminDir(s.AdminDir, s.GID); err != nil {
		return "", err
	}
	if _, err := r.ensureBridge(ctx, s.Name, s.labels()); err != nil {
		return "", err
	}
	for _, v := range []string{s.Name + "-data", s.Name + "-config"} {
		if _, err := r.cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: v, Labels: s.labels()}); err != nil {
			return "", fmt.Errorf("create volume %s: %w", v, err)
		}
	}

	id, running, err := r.currentEdge(ctx, s)
	if err != nil {
		return "", err
	}
	if id == "" {
		if err := r.pullIfMissing(ctx, s.Image); err != nil {
			return "", err
		}
		res, err := r.cli.ContainerCreate(ctx, s.createOptions())
		if err != nil {
			return "", fmt.Errorf("create edge %s: %w", s.Name, wrap(err))
		}
		id = res.ID
	}
	if !running {
		// A socket left by a stopped Caddy would block its listener.
		if err := os.Remove(s.AdminSocket()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("remove stale admin socket: %w", err)
		}
		if _, err := r.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
			return "", fmt.Errorf("start edge %s: %w", s.Name, wrap(err))
		}
	}
	nets, err := r.cli.NetworkList(ctx, client.NetworkListOptions{Filters: make(client.Filters).Add("label", labelManaged+"=true")})
	if err != nil {
		return "", fmt.Errorf("list networks: %w", err)
	}
	for _, n := range nets.Items {
		if slugRE.MatchString(n.Labels[labelApp]) && n.Name == NetworkName(n.Labels[labelApp]) {
			if err := r.attachEdge(ctx, s.Name, n.Name); err != nil {
				return "", err
			}
		}
	}
	if err := waitSocket(ctx, s.AdminSocket(), 30*time.Second); err != nil {
		return "", fmt.Errorf("edge %s: admin socket: %w", s.Name, err)
	}
	return id, nil
}

// currentEdge returns the existing edge container if it matches spec. A
// container built from another spec is removed; a foreign one is refused.
func (r *Runtime) currentEdge(ctx context.Context, s EdgeSpec) (id string, running bool, err error) {
	got, err := r.cli.ContainerInspect(ctx, s.Name, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect edge %s: %w", s.Name, err)
	}
	c := got.Container
	if c.Config == nil || !hasLabels(c.Config.Labels, s.labels()) {
		return "", false, fmt.Errorf("container %s: %w", s.Name, ErrNotManaged)
	}
	if c.Config.Labels[labelSpec] != s.hash() {
		if _, err := r.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			return "", false, fmt.Errorf("replace edge %s: %w", s.Name, err)
		}
		return "", false, nil
	}
	return c.ID, c.State != nil && c.State.Running, nil
}

// AttachEdge joins the edge container to an app's network.
func (r *Runtime) AttachEdge(ctx context.Context, edge, app string) error {
	if _, err := r.ensureBridge(ctx, NetworkName(app), map[string]string{labelManaged: "true", labelApp: app}); err != nil {
		return err
	}
	return r.attachEdge(ctx, edge, NetworkName(app))
}

func (r *Runtime) attachEdge(ctx context.Context, edge, netName string) error {
	got, err := r.cli.ContainerInspect(ctx, edge, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect edge %s: %w", edge, wrap(err))
	}
	c := got.Container
	if c.Config == nil || c.Config.Labels[labelRole] != roleEdge || c.Config.Labels[labelManaged] != "true" {
		return fmt.Errorf("container %s: %w", edge, ErrNotManaged)
	}
	if c.NetworkSettings != nil && c.NetworkSettings.Networks[netName] != nil {
		return nil
	}
	if _, err := r.cli.NetworkConnect(ctx, netName, client.NetworkConnectOptions{Container: c.ID}); err != nil {
		return fmt.Errorf("attach edge to %s: %w", netName, wrap(err))
	}
	return nil
}

// RemoveEdge removes the edge container and its network, and with volumes
// also its certificates and saved config (tests, uninstall).
func (r *Runtime) RemoveEdge(ctx context.Context, name string, volumes bool) error {
	got, err := r.cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	switch {
	case cerrdefs.IsNotFound(err):
	case err != nil:
		return fmt.Errorf("inspect edge %s: %w", name, err)
	case got.Container.Config == nil || got.Container.Config.Labels[labelRole] != roleEdge:
		return fmt.Errorf("container %s: %w", name, ErrNotManaged)
	default:
		if _, err := r.cli.ContainerRemove(ctx, got.Container.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove edge %s: %w", name, err)
		}
	}
	if _, err := r.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove edge network %s: %w", name, err)
	}
	if volumes {
		for _, v := range []string{name + "-data", name + "-config"} {
			if _, err := r.cli.VolumeRemove(ctx, v, client.VolumeRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
				return fmt.Errorf("remove volume %s: %w", v, err)
			}
		}
	}
	return nil
}

func (r *Runtime) pullIfMissing(ctx context.Context, ref string) error {
	if _, err := r.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspect image %s: %w", ref, err)
	}
	resp, err := r.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	if err := resp.Wait(ctx); err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	return nil
}

// prepareAdminDir makes dir exist as group gid with mode 2770: sockets
// created in it inherit the group (setgid), and nobody else can enter it.
// A directory the worker does not own (e.g. made by systemd) must already
// be set up that way.
func prepareAdminDir(dir string, gid int) error {
	if err := os.MkdirAll(dir, 0o770); err != nil {
		return fmt.Errorf("admin directory: %w", err)
	}
	if err := os.Chown(dir, -1, gid); err == nil {
		if err := os.Chmod(dir, 0o770|fs.ModeSetgid); err != nil {
			return fmt.Errorf("admin directory: %w", err)
		}
	}
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("admin directory: %w", err)
	}
	if st.Mode()&(fs.ModeSetgid|0o777) != fs.ModeSetgid|0o770 || groupOf(st) != gid {
		return fmt.Errorf("admin directory %s must be group %d with mode 2770, is group %d mode %v", dir, gid, groupOf(st), st.Mode())
	}
	return nil
}

func waitSocket(ctx context.Context, path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	for {
		c, err := d.DialContext(ctx, "unix", path)
		if err == nil {
			return c.Close()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("not accepting connections after %s: %w", timeout, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
