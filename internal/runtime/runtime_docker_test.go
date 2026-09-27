//go:build docker

package runtime

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// These tests need Docker Engine and a host that reaches bridge IPs (Linux or
// WSL2, not Docker Desktop). They run on the owner's machine (`make
// test-docker`), not in CI (CLAUDE.md §6). TestMain builds two tiny probe
// images FROM scratch, so nothing is pulled; every test removes its own
// containers and network.

var probeImage, otherImage string

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	dir, err := os.MkdirTemp("", "shipyard-probe-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	gobin, err := exec.LookPath("go")
	if err != nil {
		return 0, err
	}
	build := exec.Command(gobin, "build", "-o", filepath.Join(dir, "probe"), "./testdata/probe")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := build.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("build probe: %v\n%s", err, out)
	}
	tag := "shipyard-test/probe:" + strings.ToLower(rand.Text()[:8])
	var ids []string
	for _, variant := range []string{"a", "b"} {
		df := "FROM scratch\nCOPY probe /probe\nEXPOSE 8080\nLABEL variant=" + variant + "\nENTRYPOINT [\"/probe\"]\n"
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(df), 0o644); err != nil {
			return 0, err
		}
		if out, err := exec.Command("docker", "build", "-q", "-t", tag+variant, dir).CombinedOutput(); err != nil {
			return 0, fmt.Errorf("docker build: %v\n%s", err, out)
		}
		defer exec.Command("docker", "image", "rm", "--force", tag+variant).Run()
		out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", tag+variant).Output()
		if err != nil {
			return 0, fmt.Errorf("inspect probe image: %v", err)
		}
		ids = append(ids, strings.TrimSpace(string(out)))
	}
	probeImage, otherImage = ids[0], ids[1]
	return m.Run(), nil
}

func newRuntime(t *testing.T) *Runtime {
	t.Helper()
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// testApp returns a fresh slug and removes everything labelled with it, plus
// its network, when the test ends.
func testApp(t *testing.T) string {
	t.Helper()
	app := "rt-" + strings.ToLower(rand.Text()[:8])
	t.Cleanup(func() {
		out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+labelApp+"="+app).Output()
		for _, id := range strings.Fields(string(out)) {
			exec.Command("docker", "rm", "--force", "--volumes", id).Run()
		}
		exec.Command("docker", "network", "rm", NetworkName(app)).Run()
	})
	return app
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func spec(app, image string) Spec {
	return Spec{
		App:          app,
		DeploymentID: newUUID(),
		Commit:       strings.Repeat("ab", 20),
		Image:        image,
		CPUs:         0.5,
		Memory:       64 << 20,
		PidsLimit:    64,
		StopTimeout:  3 * time.Second,
	}
}

func dockerOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fetch GETs a probe path by container IP, waiting for the server to listen.
func fetch(t *testing.T, ip netip.Addr, path string) string {
	t.Helper()
	url := "http://" + netip.AddrPortFrom(ip, 8080).String() + path
	hc := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := hc.Get(url)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: %s: %s", path, resp.Status, b)
			}
			return strings.TrimSpace(string(b))
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s: %v", path, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func startHardened(t *testing.T, r *Runtime, s Spec) string {
	t.Helper()
	ctx := t.Context()
	id, err := r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Phase 1 exit criterion: docker inspect confirms CapDrop=ALL,
// no-new-privileges, the limits, the local log driver, and no published
// ports; the probe confirms the kernel enforces them inside the container.
func TestHardenedContainer(t *testing.T) {
	r := newRuntime(t)
	app := testApp(t)
	s := spec(app, probeImage)
	s.Env = map[string]string{"GREETING": "hello world", "EMPTY": ""}
	id := startHardened(t, r, s)

	got, err := r.cli.ContainerInspect(t.Context(), id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c, h := got.Container, got.Container.HostConfig
	if !slices.Equal(h.CapDrop, []string{"ALL"}) || len(h.CapAdd) != 0 {
		t.Errorf("caps drop=%v add=%v", h.CapDrop, h.CapAdd)
	}
	if !slices.Contains(h.SecurityOpt, "no-new-privileges") {
		t.Errorf("security opts = %v", h.SecurityOpt)
	}
	if h.Memory != 64<<20 || h.NanoCPUs != 500_000_000 || h.PidsLimit == nil || *h.PidsLimit != 64 {
		t.Errorf("limits memory=%d nanocpus=%d pids=%v", h.Memory, h.NanoCPUs, h.PidsLimit)
	}
	if h.LogConfig.Type != "local" {
		t.Errorf("log driver = %q", h.LogConfig.Type)
	}
	if h.RestartPolicy.Name != container.RestartPolicyUnlessStopped {
		t.Errorf("restart = %q", h.RestartPolicy.Name)
	}
	if h.Privileged || h.PublishAllPorts || len(h.PortBindings) != 0 || len(h.Binds) != 0 || len(c.Mounts) != 0 {
		t.Errorf("privileged=%v publishAll=%v bindings=%v binds=%v mounts=%v",
			h.Privileged, h.PublishAllPorts, h.PortBindings, h.Binds, c.Mounts)
	}
	// The image EXPOSEs 8080; it must still not be bound on the host.
	for port, bindings := range c.NetworkSettings.Ports {
		if len(bindings) != 0 {
			t.Errorf("port %v published as %v", port, bindings)
		}
	}
	if len(c.NetworkSettings.Networks) != 1 || c.NetworkSettings.Networks[NetworkName(app)] == nil {
		t.Errorf("networks = %v, want only %s", c.NetworkSettings.Networks, NetworkName(app))
	}
	if c.Image != probeImage || c.Name != "/"+ContainerName(app, s.DeploymentID) {
		t.Errorf("image=%s name=%s", c.Image, c.Name)
	}

	// The same facts through the docker CLI, as the exit criterion states.
	cli := dockerOut(t, "inspect", "--format",
		"{{json .HostConfig.CapDrop}} {{json .HostConfig.SecurityOpt}} {{.HostConfig.LogConfig.Type}} "+
			"{{.HostConfig.Memory}} {{.HostConfig.NanoCpus}} {{json .HostConfig.PidsLimit}} "+
			"{{.HostConfig.Privileged}} {{.HostConfig.PublishAllPorts}}", id)
	if want := `["ALL"] ["no-new-privileges"] local 67108864 500000000 64 false false`; cli != want {
		t.Errorf("docker inspect = %q\nwant %q", cli, want)
	}

	st, err := r.Inspect(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.App != app || st.DeploymentID != s.DeploymentID || !st.IP.IsValid() {
		t.Fatalf("state = %+v", st)
	}

	// Inside the container.
	status := fetch(t, st.IP, "/read?path=/proc/self/status")
	for _, want := range []string{"NoNewPrivs:\t1", "CapPrm:\t0000000000000000", "CapEff:\t0000000000000000", "CapBnd:\t0000000000000000"} {
		if !strings.Contains(status, want) {
			t.Errorf("/proc/self/status lacks %q", want)
		}
	}
	for path, want := range map[string]string{
		"/sys/fs/cgroup/pids.max":   "64",
		"/sys/fs/cgroup/memory.max": "67108864",
		"/sys/fs/cgroup/cpu.max":    "50000 100000",
	} {
		if got := fetch(t, st.IP, "/read?path="+path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if got := fetch(t, st.IP, "/env?key=GREETING"); got != "hello world" {
		t.Errorf("GREETING = %q", got)
	}
	if got := fetch(t, st.IP, "/env?key=EMPTY"); got != "" {
		t.Errorf("EMPTY = %q", got)
	}
}

func TestAllowlistedCapability(t *testing.T) {
	r := newRuntime(t)
	s := spec(testApp(t), probeImage)
	s.CapAdd = []string{"NET_BIND_SERVICE"}
	id := startHardened(t, r, s)
	st, err := r.Inspect(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	// CAP_NET_BIND_SERVICE is bit 10.
	if status := fetch(t, st.IP, "/read?path=/proc/self/status"); !strings.Contains(status, "CapBnd:\t0000000000000400") {
		t.Errorf("bounding set is not exactly NET_BIND_SERVICE:\n%s", status)
	}
}

func TestCreateIsIdempotent(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	s := spec(testApp(t), probeImage)

	// Create brings its own network.
	id1, err := r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if got := dockerOut(t, "network", "inspect", "--format", `{{index .Labels "io.shipyard.app"}}`, NetworkName(s.App)); got != s.App {
		t.Fatalf("network label = %q", got)
	}
	id2, err := r.Create(ctx, s)
	if err != nil || id2 != id1 {
		t.Fatalf("second create = %s, %v; want %s", id2, err, id1)
	}
	s.Image = otherImage
	if _, err := r.Create(ctx, s); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("create with another image: err = %v, want ErrNameTaken", err)
	}
	s.Image = "sha256:" + strings.Repeat("0", 64)
	s.DeploymentID = newUUID()
	if _, err := r.Create(ctx, s); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create from a missing image: err = %v, want ErrNotFound", err)
	}
}

func TestLifecycleIsIdempotent(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	id := startHardened(t, r, spec(testApp(t), probeImage))

	if err := r.Start(ctx, id); err != nil {
		t.Fatalf("start a running container: %v", err)
	}
	var lines []string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if lines, _ = r.Logs(ctx, id, 5); len(lines) > 0 {
			break
		}
	}
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "probe listening on :8080") {
		t.Fatalf("logs = %q", lines)
	}
	for range 2 {
		if err := r.Stop(ctx, id, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	st, err := r.Inspect(ctx, id)
	if err != nil || st.Running || st.Status != "exited" {
		t.Fatalf("after stop: %+v, %v", st, err)
	}
	for range 2 {
		if err := r.Remove(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Inspect(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inspect removed: err = %v", err)
	}
	if err := r.Start(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("start removed: err = %v", err)
	}
	if err := r.Stop(ctx, id, time.Second); err != nil {
		t.Fatalf("stop removed: %v", err)
	}
}

func TestEnsureNetwork(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	app := testApp(t)
	id1, err := r.EnsureNetwork(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := r.EnsureNetwork(ctx, app)
	if err != nil || id2 != id1 {
		t.Fatalf("second ensure = %s, %v; want %s", id2, err, id1)
	}
	got := dockerOut(t, "network", "inspect", "--format",
		`{{.Driver}} {{index .Labels "io.shipyard.managed"}} {{index .Labels "io.shipyard.app"}}`, NetworkName(app))
	if got != "bridge true "+app {
		t.Fatalf("network = %q", got)
	}
	for range 2 {
		if err := r.RemoveNetwork(ctx, app); err != nil {
			t.Fatal(err)
		}
	}
}

// Shipyard never adopts, starts, stops, or removes what it did not create.
func TestRefusesUnmanaged(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	app := testApp(t)

	dockerOut(t, "network", "create", NetworkName(app))
	if _, err := r.EnsureNetwork(ctx, app); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("ensure over a foreign network: err = %v", err)
	}
	if err := r.RemoveNetwork(ctx, app); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("remove a foreign network: err = %v", err)
	}
	if _, err := r.Create(ctx, spec(app, probeImage)); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("create on a foreign network: err = %v", err)
	}

	name := "shipyard-test-foreign-" + app
	id := dockerOut(t, "create", "--name", name, "--network", "none", probeImage)
	t.Cleanup(func() { exec.Command("docker", "rm", "--force", name).Run() })
	if err := r.Start(ctx, id); !errors.Is(err, ErrNotManaged) {
		t.Errorf("start: err = %v", err)
	}
	if _, err := r.Inspect(ctx, id); !errors.Is(err, ErrNotManaged) {
		t.Errorf("inspect: err = %v", err)
	}
	if err := r.Stop(ctx, id, time.Second); !errors.Is(err, ErrNotManaged) {
		t.Errorf("stop: err = %v", err)
	}
	if err := r.Remove(ctx, id); !errors.Is(err, ErrNotManaged) {
		t.Errorf("remove: err = %v", err)
	}
	dockerOut(t, "inspect", "--format", "{{.Id}}", id) // still there

	// A foreign container holding the deterministic name is not adopted.
	s := spec(app, probeImage)
	dockerOut(t, "network", "rm", NetworkName(app))
	if _, err := r.EnsureNetwork(ctx, app); err != nil {
		t.Fatal(err)
	}
	squat := ContainerName(app, s.DeploymentID)
	dockerOut(t, "create", "--name", squat, "--network", "none", probeImage)
	t.Cleanup(func() { exec.Command("docker", "rm", "--force", squat).Run() })
	if _, err := r.Create(ctx, s); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("create over a foreign container: err = %v", err)
	}
}
