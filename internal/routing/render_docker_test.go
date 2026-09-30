//go:build docker

package routing

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"io"
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
)

// A rendered config loads into a real Caddy (the P2.1 edge) and routes:
// app, idle app, API paths, and nothing else. Caddy's internal CA stands in
// for ACME, and upstreams are `caddy respond` containers, so nothing is
// pulled beyond the edge image.
func TestRenderedConfigServes(t *testing.T) {
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
	upstream := "shipyard-test-up-" + id
	t.Cleanup(func() {
		exec.Command("docker", "rm", "--force", upstream).Run()
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
	// The app: a tiny HTTP server on the app network, reachable only there.
	out, err := exec.Command("docker", "run", "-d", "--name", upstream, "--network", runtime.NetworkName(app),
		// The image's caddy binary needs NET_BIND_SERVICE even to exec [CADDY-IMAGE].
		"--cap-drop", "ALL", "--cap-add", "NET_BIND_SERVICE", "--security-opt", "no-new-privileges",
		"--entrypoint", "caddy", runtime.DefaultEdgeImage, "respond", "--listen", ":3000", "--body", "app ok").CombinedOutput()
	if err != nil {
		t.Fatalf("start upstream: %v\n%s", err, out)
	}

	// A fresh install renders no routes at all; Caddy must accept that too.
	empty, err := Render(Settings{AdminSocket: edge.AdminSocket(), CA: CAInternal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := adminCall(t, edge.AdminSocket(), http.MethodPost, "/load", string(empty)); code != 200 {
		t.Fatalf("POST /load (empty) = %d %s", code, body)
	}

	cfg, err := Render(Settings{AdminSocket: edge.AdminSocket(), CA: CAInternal,
		APIHostname: "api.test.example", APIUpstream: upstream + ":3000"},
		[]Route{{Hostname: "web.test.example", Upstream: upstream + ":3000"}, {Hostname: "idle.test.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := adminCall(t, edge.AdminSocket(), http.MethodPost, "/load", string(cfg)); code != 200 {
		t.Fatalf("POST /load = %d %s", code, body)
	}
	// The admin API stayed on the socket after the load (invariant 12).
	if code, _ := adminCall(t, edge.AdminSocket(), http.MethodGet, "/config/admin/listen", ""); code != 200 {
		t.Fatalf("admin socket gone after load: %d", code)
	}

	https := hostPort(t, edge.Name, "443/tcp")
	for _, tc := range []struct {
		host, path string
		code       int
		body       string
	}{
		{"web.test.example", "/", 200, "app ok"},
		{"idle.test.example", "/", 503, NoDeploymentBody},
		{"api.test.example", "/v1/whoami", 200, "app ok"},
		{"api.test.example", "/hooks/github", 200, "app ok"},
		{"api.test.example", "/admin", 404, ""},
		{"api.test.example", "/v1", 404, ""},
	} {
		code, body := get(t, https, tc.host, tc.path)
		if code != tc.code || body != tc.body {
			t.Errorf("https://%s%s = %d %q, want %d %q", tc.host, tc.path, code, body, tc.code, tc.body)
		}
	}
	// An unknown hostname gets no certificate at all.
	if _, err := tlsDial(https, "other.test.example"); err == nil {
		t.Error("TLS handshake for an unrouted hostname succeeded")
	}
	// Plain HTTP redirects to HTTPS (automatic HTTPS added the :80 server).
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+hostPort(t, edge.Name, "80/tcp")+"/", nil)
	req.Host = "web.test.example"
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "https://web.test.example/" {
		t.Errorf("http:// = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func adminCall(t *testing.T, socket, method, path, body string) (int, string) {
	t.Helper()
	hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}}
	req, _ := http.NewRequestWithContext(t.Context(), method, "http://caddy"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("admin %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func hostPort(t *testing.T, container, port string) string {
	t.Helper()
	out, err := exec.Command("docker", "port", container, port).Output()
	if err != nil {
		t.Fatalf("docker port %s: %v", port, err)
	}
	_, p, _ := net.SplitHostPort(strings.Fields(string(out))[0])
	return p
}

func tlsDial(port, sni string) (*tls.Conn, error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return tls.DialWithDialer(d, "tcp", "127.0.0.1:"+port, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
}

// get requests https://host/path through the published port. The internal
// CA's certificate is not trusted here; the test checks routing, not trust.
func get(t *testing.T, port, host, path string) (int, string) {
	t.Helper()
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+port)
		}}}
	var last error
	// Caddy issues the internal certificates just after the load.
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		resp, err := hc.Get("https://" + host + path)
		if err != nil {
			last = err
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	t.Fatalf("https://%s%s: %v", host, path, last)
	return 0, ""
}
