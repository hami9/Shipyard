//go:build docker

package routing

import (
	"context"
	"crypto/rand"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/runtime"
	"github.com/hami9/shipyard/internal/store"
)

// P2.4 against a real Caddy: a switch moves one app's hostname to a new
// upstream on the app network, verified over the verify socket, and served
// over HTTPS; Release goes back to the table; an unreachable candidate
// fails verification.
func TestRouterAgainstCaddy(t *testing.T) {
	ctx := t.Context()
	rt, err := runtime.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	id := strings.ToLower(rand.Text()[:8])
	edge := runtime.EdgeSpec{Name: "shipyard-test-edge-" + id, Image: runtime.DefaultEdgeImage,
		AdminDir: filepath.Join(t.TempDir(), "admin"), GID: os.Getgid(), BindIP: netip.MustParseAddr("127.0.0.1")}
	app := "rt-" + id
	v1, v2 := "shipyard-test-v1-"+id, "shipyard-test-v2-"+id
	t.Cleanup(func() {
		exec.Command("docker", "rm", "--force", v1, v2).Run()
		exec.Command("docker", "network", "disconnect", "--force", runtime.NetworkName(app), edge.Name).Run()
		exec.Command("docker", "network", "rm", runtime.NetworkName(app)).Run()
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		rt.RemoveEdge(c, edge.Name, true)
	})
	if _, err := rt.EnsureEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}
	rt.Edge = edge.Name
	if _, err := rt.EnsureNetwork(ctx, app); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{v1: "v1", v2: "v2"} {
		out, err := exec.Command("docker", "run", "-d", "--name", name, "--network", runtime.NetworkName(app),
			"--cap-drop", "ALL", "--cap-add", "NET_BIND_SERVICE", "--security-opt", "no-new-privileges",
			"--entrypoint", "caddy", runtime.DefaultEdgeImage, "respond", "--listen", ":3000", "--body", body).CombinedOutput()
		if err != nil {
			t.Fatalf("start %s: %v\n%s", name, err, out)
		}
	}

	dep := "d1"
	routes := &fakeRoutes{rows: []store.Route{{AppID: "a1", Hostname: "web.test.example", DeploymentID: &dep, Upstream: v1 + ":3000"}}}
	verify := filepath.Join(edge.AdminDir, VerifySocketName)
	r, err := NewRouter(routes, Settings{AdminSocket: edge.AdminSocket(), VerifySocket: verify, CA: CAInternal})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	// The verify socket is as private as the admin socket.
	if fi, err := os.Stat(verify); err != nil || fi.Mode().Type() != fs.ModeSocket || fi.Mode().Perm() != 0o660 {
		t.Fatalf("verify socket: %v %v", fi, err)
	}
	if got := viaVerify(t, verify, "web.test.example"); got != "v1" {
		t.Fatalf("before the switch = %q", got)
	}

	hosts, err := r.Switch(ctx, "a1", v2+":3000", "/")
	if err != nil || len(hosts) != 1 {
		t.Fatalf("Switch = %v, %v", hosts, err)
	}
	if got := viaVerify(t, verify, "web.test.example"); got != "v2" {
		t.Fatalf("verify socket after the switch = %q", got)
	}
	// The public HTTPS server switched in the same load.
	if code, body := get(t, hostPort(t, edge.Name, "443/tcp"), "web.test.example", "/"); code != 200 || body != "v2" {
		t.Fatalf("https after the switch = %d %q", code, body)
	}

	if _, err := r.Release(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if got := viaVerify(t, verify, "web.test.example"); got != "v1" {
		t.Fatalf("after Release = %q", got)
	}

	// A candidate Caddy cannot reach (no such container) fails the check.
	start := time.Now()
	if _, err := r.Switch(ctx, "a1", "shipyard-test-missing-"+id+":3000", "/"); err == nil || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("unreachable candidate: %v", err)
	}
	t.Logf("failed verification took %s", time.Since(start).Round(time.Millisecond))
	if _, err := r.Release(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if got := viaVerify(t, verify, "web.test.example"); got != "v1" {
		t.Fatalf("after the failed switch = %q", got)
	}
}

func viaVerify(t *testing.T, socket, host string) string {
	t.Helper()
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}}
	resp, err := hc.Get("http://" + host + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
