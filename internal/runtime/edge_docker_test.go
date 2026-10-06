//go:build docker

package runtime

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// newEdge returns a spec with a unique name, a private admin directory,
// loopback-only random ports, and cleanup of everything it creates.
func newEdge(t *testing.T, r *Runtime) EdgeSpec {
	t.Helper()
	s := EdgeSpec{Name: "shipyard-test-edge-" + strings.ToLower(rand.Text()[:8]), Image: DefaultEdgeImage,
		AdminDir: filepath.Join(t.TempDir(), "admin"), GID: os.Getgid(), BindIP: netip.MustParseAddr("127.0.0.1")}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := r.RemoveEdge(ctx, s.Name, true); err != nil {
			t.Errorf("remove edge: %v", err)
		}
	})
	return s
}

// admin calls Caddy's admin API over the socket.
func admin(t *testing.T, s EdgeSpec, method, path, body string) (int, string) {
	t.Helper()
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", s.AdminSocket())
		}}}
	req, _ := http.NewRequestWithContext(t.Context(), method, "http://caddy"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("admin %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func publishedPort(t *testing.T, r *Runtime, name, port string) string {
	t.Helper()
	got, err := r.cli.ContainerInspect(t.Context(), name, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b := got.Container.NetworkSettings.Ports[network.MustParsePort(port)]
	if len(b) == 0 || b[0].HostIP.String() != "127.0.0.1" {
		t.Fatalf("%s bindings = %v", port, b)
	}
	return b[0].HostPort
}

// P5.8b: the admin socket's group is one of the caller's supplementary
// groups (shipyard-caddy in production), not its primary group (shipyard,
// shared with the API); Caddy also gets the primary group, for the API's
// socket.
func TestEdgeAdminGroup(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	groups, _ := os.Getgroups()
	admin := -1
	for _, g := range groups {
		if g != os.Getegid() && g != 0 {
			admin = g
			break
		}
	}
	if admin < 0 {
		t.Skip("needs a supplementary group (e.g. docker)")
	}
	s := newEdge(t, r)
	s.GID, s.APIGID = admin, os.Getegid()
	s.APISocketDir = t.TempDir()
	id, err := r.EnsureEdge(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.Stat(s.AdminDir)
	if err != nil || groupOf(dir) != admin || dir.Mode()&(fs.ModeSetgid|0o777) != fs.ModeSetgid|0o770 {
		t.Fatalf("admin dir group %d mode %v, %v; want group %d, 2770", groupOf(dir), dir.Mode(), err, admin)
	}
	sock, err := os.Stat(s.AdminSocket())
	if err != nil || groupOf(sock) != admin || sock.Mode().Perm() != 0o660 {
		t.Fatalf("admin socket group %d mode %v, %v", groupOf(sock), sock.Mode(), err)
	}
	got, _ := r.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	c := got.Container
	if c.Config.User != "0:"+strconv.Itoa(admin) || !slices.Equal(c.HostConfig.GroupAdd, []string{strconv.Itoa(os.Getegid())}) {
		t.Fatalf("user %q, group-add %v", c.Config.User, c.HostConfig.GroupAdd)
	}
	// Inside, Caddy's process has both groups.
	out, err := exec.Command("docker", "exec", s.Name, "id", "-G").CombinedOutput()
	ids := strings.Fields(string(out))
	if err != nil || !slices.Contains(ids, strconv.Itoa(admin)) || !slices.Contains(ids, strconv.Itoa(os.Getegid())) {
		t.Fatalf("groups in the container: %q, %v", out, err)
	}
}

func TestEdgeBootstrap(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	s := newEdge(t, r)
	id, err := r.EnsureEdge(ctx, s)
	if err != nil {
		t.Fatal(err)
	}

	// Invariant 12: the admin socket, group-owned, 0660, in a 2770 directory.
	fi, err := os.Stat(s.AdminSocket())
	if err != nil || fi.Mode().Type() != fs.ModeSocket || fi.Mode().Perm() != 0o660 || groupOf(fi) != s.GID {
		t.Fatalf("admin socket = %v mode %v group %d, %v", s.AdminSocket(), fi.Mode(), groupOf(fi), err)
	}
	if code, body := admin(t, s, http.MethodGet, "/config/", ""); code != 200 || body != "null" {
		t.Fatalf("GET /config/ = %d %q, want an empty config", code, body)
	}

	// No TCP admin listener, not even inside the container's network.
	got, _ := r.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	ip := got.Container.NetworkSettings.Networks[s.Name].IPAddress
	if c, err := net.DialTimeout("tcp", netip.AddrPortFrom(ip, 2019).String(), 2*time.Second); err == nil {
		c.Close()
		t.Fatal("the admin API listens on TCP 2019")
	}

	// Hardening, and the only host path is the socket directory.
	h := got.Container.HostConfig
	// Engine 29 reports added capabilities with the CAP_ prefix [MOBY-CLIENT].
	if !slices.Equal(h.CapDrop, []string{"ALL"}) || !slices.Equal(h.CapAdd, []string{"CAP_NET_BIND_SERVICE"}) ||
		!slices.Contains(h.SecurityOpt, "no-new-privileges") || !h.ReadonlyRootfs || h.Privileged || h.LogConfig.Type != "local" {
		t.Errorf("edge hardening: %+v", h)
	}
	for _, m := range got.Container.Mounts {
		if m.Type == "bind" && m.Source != s.AdminDir {
			t.Errorf("unexpected bind mount %s", m.Source)
		}
	}

	// A loaded config serves on the published port (invariant 11).
	cfg := `{"apps":{"http":{"servers":{"s":{"listen":[":80"],"routes":[{"handle":[{"handler":"static_response","body":"edge ok"}]}]}}}}}`
	if code, body := admin(t, s, http.MethodPost, "/load", cfg); code != 200 {
		t.Fatalf("POST /load = %d %s", code, body)
	}
	httpPort := publishedPort(t, r, s.Name, "80/tcp")
	if got := fetchURL(t, "http://127.0.0.1:"+httpPort+"/"); got != "edge ok" {
		t.Fatalf("GET through the published port = %q", got)
	}

	// Idempotent: same container, still running.
	if again, err := r.EnsureEdge(ctx, s); err != nil || again != id {
		t.Fatalf("second ensure = %s, %v; want %s", again, err, id)
	}

	// A restart resumes the loaded config (--resume and the config volume),
	// and Caddy listens on the socket again.
	dockerOut(t, "restart", "--timeout", "5", s.Name)
	if err := waitSocket(ctx, s.AdminSocket(), 30*time.Second); err != nil {
		t.Fatal(err)
	}
	httpPort = publishedPort(t, r, s.Name, "80/tcp")
	if got := fetchURL(t, "http://127.0.0.1:"+httpPort+"/"); got != "edge ok" {
		t.Fatalf("after restart = %q", got)
	}

	// A changed spec recreates the container but keeps the saved config.
	s2 := s
	s2.Image = strings.Split(DefaultEdgeImage, "@")[0] // same image, another reference
	id2, err := r.EnsureEdge(ctx, s2)
	if err != nil || id2 == id {
		t.Fatalf("recreate = %s, %v", id2, err)
	}
	if code, body := admin(t, s2, http.MethodGet, "/config/apps/http/servers/s/routes/0/handle/0/body", ""); code != 200 || body != `"edge ok"` {
		t.Fatalf("config after recreate = %d %s", code, body)
	}
}

// ADR-0003: the edge joins every app network, existing or new, and never
// adopts a foreign container.
func TestEdgeJoinsAppNetworks(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	s := newEdge(t, r)
	before := testApp(t)
	if _, err := r.EnsureNetwork(ctx, before); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EnsureEdge(ctx, s); err != nil {
		t.Fatal(err)
	}
	r.Edge = s.Name
	after := testApp(t)
	// Runs before testApp's cleanups: a network with the edge attached
	// cannot be removed.
	t.Cleanup(func() {
		for _, app := range []string{before, after} {
			exec.Command("docker", "network", "disconnect", "--force", NetworkName(app), s.Name).Run()
		}
	})
	if _, err := r.Create(ctx, spec(after, probeImage)); err != nil {
		t.Fatal(err)
	}
	nets := dockerOut(t, "inspect", "--format", "{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}", s.Name)
	for _, want := range []string{s.Name, NetworkName(before), NetworkName(after)} {
		if !slices.Contains(strings.Fields(nets), want) {
			t.Errorf("edge networks %q lack %s", nets, want)
		}
	}
	// Removing an app network detaches the edge first.
	if err := r.RemoveNetwork(ctx, before); err != nil {
		t.Fatal(err)
	}
}

func TestEdgeRefusesForeignContainer(t *testing.T) {
	r := newRuntime(t)
	s := newEdge(t, r)
	dockerOut(t, "create", "--name", s.Name, "--network", "none", probeImage)
	t.Cleanup(func() { exec.Command("docker", "rm", "--force", s.Name).Run() })
	if _, err := r.EnsureEdge(t.Context(), s); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("err = %v", err)
	}
	if err := r.RemoveEdge(t.Context(), s.Name, false); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("remove foreign: %v", err)
	}
	if err := r.ArchiveEdgeData(t.Context(), s.Name, io.Discard); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("archive foreign: %v", err)
	}
}

// P3.5: the backup reads Caddy's data volume as a tar stream through the
// archive API, with file modes kept, from a running or a stopped edge [DK-CP].
func TestArchiveEdgeData(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	s := newEdge(t, r)
	if err := r.ArchiveEdgeData(ctx, s.Name, io.Discard); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no edge yet: %v", err)
	}
	if _, err := r.EnsureEdge(ctx, s); err != nil {
		t.Fatal(err)
	}
	dockerOut(t, "exec", s.Name, "sh", "-c",
		"mkdir -p /data/caddy/certificates && echo private > /data/caddy/certificates/test.key && chmod 600 /data/caddy/certificates/test.key")
	check := func(state string) {
		t.Helper()
		var buf bytes.Buffer
		if err := r.ArchiveEdgeData(ctx, s.Name, &buf); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		tr := tar.NewReader(&buf)
		for {
			h, err := tr.Next()
			if err != nil {
				t.Fatalf("%s: test.key is not in the archive: %v", state, err)
			}
			if h.Name != "data/caddy/certificates/test.key" {
				continue
			}
			body, _ := io.ReadAll(tr)
			if string(body) != "private\n" || h.Mode&0o777 != 0o600 {
				t.Fatalf("%s: test.key = %q, mode %o", state, body, h.Mode&0o777)
			}
			return
		}
	}
	check("running")
	dockerOut(t, "stop", "--timeout", "1", s.Name)
	check("stopped")

	// P3.6b: the archive goes into a fresh edge on a "new host": the edge is
	// created, the file arrives with its mode, owned by root, and Caddy runs
	// again with its admin socket up. A second restore over it works too.
	var buf bytes.Buffer
	if err := r.ArchiveEdgeData(ctx, s.Name, &buf); err != nil {
		t.Fatal(err)
	}
	fresh := newEdge(t, r)
	for range 2 {
		if err := r.RestoreEdgeData(ctx, fresh, bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatal(err)
		}
		got := dockerOut(t, "exec", fresh.Name, "sh", "-c", "stat -c '%a %u:%g' /data/caddy/certificates/test.key && cat /data/caddy/certificates/test.key")
		if got != "600 0:0\nprivate" {
			t.Fatalf("restored file = %q", got)
		}
		if code, _ := admin(t, fresh, "GET", "/config/", ""); code != http.StatusOK {
			t.Fatalf("admin API after the restore: %d", code)
		}
	}
	if err := r.RestoreEdgeData(ctx, fresh, strings.NewReader("not a tar")); err == nil {
		t.Fatal("garbage was restored")
	}
}

func fetchURL(t *testing.T, url string) string {
	t.Helper()
	hc := &http.Client{Timeout: 5 * time.Second}
	var last error
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		resp, err := hc.Get(url)
		if err != nil {
			last = err
			continue
		}
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		resp.Body.Close()
		return b.String()
	}
	t.Fatalf("GET %s: %v", url, last)
	return ""
}
