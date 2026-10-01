//go:build e2e

package e2e

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store/storetest"
)

// The restore drill (P3.6, ADR-0006; the runbook is docs/RESTORE.md).
//
// An app is deployed and backed up. Then the host is "lost": both services
// stop, and the app's container and images, the Caddy container with its
// volumes, the networks, and the builder are removed; the new host has an
// empty database and an empty KEK directory. After the KEK is copied back
// and `shipyard-worker restore` has run, starting the services must bring
// the app back by itself: the same commit, the configuration it ran with,
// the same certificate, and the same API token.
func TestRestoreDrill(t *testing.T) {
	h := start(t)
	slug := h.slug
	const host = "drill.e2e.example"
	h.cli("", "app", "create", slug, "--repo", "e2e/demo", "--branch", "main", "--port", "8080", "--health-path", "/healthz")
	h.cli("hello from the old host\n", "env", "set", slug, "GREETING")
	h.cli("", "domain", "add", slug, host)
	h.wantOp(h.deploy("--ref", h.repo.good), "succeeded", "")
	old := h.onlyRunning()
	if got := h.viaCaddy(host, "/env?key=GREETING"); got != "hello from the old host" {
		t.Fatalf("before: GREETING = %q", got)
	}
	// Changed after the deploy and never deployed: a restore brings back what
	// was running, so this value must not appear.
	h.cli("set after the deploy\n", "env", "set", slug, "GREETING")
	certBefore := h.leaf(host)
	one, keys, _ := h.backup()

	// The host is lost.
	h.stopWorker()
	h.stopAPI()
	h.docker("rm", "--force", "--volumes", old)
	h.docker("rm", "--force", h.caddy)
	h.docker("volume", "rm", h.caddy+"-data", h.caddy+"-config")
	h.docker("network", "rm", h.caddy)
	h.docker("network", "rm", "shipyard-app-"+slug)
	for _, id := range strings.Fields(h.docker("image", "ls", "-a", "-q", "--no-trunc", "--filter", "label=io.shipyard.app="+slug)) {
		h.docker("image", "rm", "--force", id)
	}
	h.docker("buildx", "rm", "--force", h.builder)

	// The new host: an empty database and an empty KEK directory.
	dir := t.TempDir()
	newKEK := filepath.Join(dir, "kek")
	if err := os.Mkdir(newKEK, 0o700); err != nil {
		t.Fatal(err)
	}
	oldDB := h.dbURL
	h.dbURL = storetest.NewDatabase(t)
	moved := []string{"SHIPYARD_DATABASE_URL=" + h.dbURL, "SHIPYARD_KEK_DIR=" + newKEK,
		"SHIPYARD_BACKUP_PG_RESTORE=" + pgTool(t, dir, "pg_restore")}
	restore := func(env ...string) (string, error) {
		cmd := exec.Command(filepath.Join(h.bin, "shipyard-worker"), "restore", "--from", one)
		cmd.Env = append(append(slices.Clone(h.workerEnv), moved...), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// Refused before anything is touched: the KEK is not there yet, and the
	// old database is not empty.
	if out, err := restore(); err == nil || !strings.Contains(out, "lacks the KEKs e2e") {
		t.Fatalf("restore without the KEK: %v\n%s", err, out)
	}
	key, err := os.ReadFile(filepath.Join(keys, "e2e.key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newKEK, "e2e.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := restore("SHIPYARD_DATABASE_URL=" + oldDB); err == nil || !strings.Contains(out, "already has tables") {
		t.Fatalf("restore into a database with tables: %v\n%s", err, out)
	}
	if exec.Command("docker", "inspect", h.caddy).Run() == nil {
		t.Fatal("a refused restore created the Caddy container")
	}
	if out, err := restore(); err != nil || !strings.Contains(out, "restore finished") {
		t.Fatalf("restore: %v\n%s", err, out)
	}

	// The runbook's next step: migrate (nothing to apply for the same
	// version, and the restored migration history must be accepted).
	run(t, dir, append(slices.Clone(h.apiEnv), moved...), filepath.Join(h.bin, "shipyard-api"), "migrate")

	// Start the services on the new host. Nothing else is done by hand.
	h.stopAPI = h.spawn("api (restored)", append(slices.Clone(h.apiEnv), moved...), "shipyard-api", "serve").stop
	waitUnix(t, h.apiSock)
	h.stopWorker = h.spawn("worker (restored)", append(slices.Clone(h.workerEnv), moved...), "shipyard-worker", "run").stop

	var now string
	for deadline := time.Now().Add(5 * time.Minute); now == "" && time.Now().Before(deadline); time.Sleep(time.Second) {
		ids := strings.Fields(h.docker("ps", "-q", "--no-trunc", "--filter", "label=io.shipyard.app="+slug))
		if len(ids) == 1 && ids[0] != old {
			now = ids[0]
		}
	}
	if now == "" {
		t.Fatalf("the app did not come back:\n%s", h.cli("", "releases", slug))
	}
	if got := h.docker("inspect", "--format", `{{index .Config.Labels "io.shipyard.commit"}}`, now); got != h.repo.good {
		t.Fatalf("rebuilt commit %s, want the active one %s", got, h.repo.good)
	}
	// The API token from the backup still works (the CLI uses it), and the
	// history shows the rebuild active and the lost deployment superseded.
	var rel string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		if rel = h.cli("", "releases", slug); strings.Contains(rel, "active") && strings.Contains(rel, "superseded") {
			break
		}
	}
	var statuses []string
	for _, line := range strings.Split(strings.TrimSpace(rel), "\n")[1:] {
		if f := strings.Fields(line); len(f) > 2 {
			statuses = append(statuses, f[1]+" "+f[2])
		}
	}
	if want := []string{"active " + h.repo.good[:12], "superseded " + h.repo.good[:12]}; !slices.Equal(statuses, want) {
		t.Fatalf("history = %v, want %v:\n%s", statuses, want, rel)
	}
	// The secret is readable again (the KEK), with the value the deployment
	// ran with, and Caddy serves the same certificate: nothing was reissued.
	if got := h.viaCaddy(host, "/env?key=GREETING"); got != "hello from the old host" {
		t.Fatalf("after the restore: GREETING = %q", got)
	}
	if got := h.viaCaddy(host, "/read?path=/etc/hostname"); got != now[:12] {
		t.Fatalf("caddy serves %q, want the rebuilt container %s", got, now[:12])
	}
	if got := h.leaf(host); got != certBefore {
		t.Fatalf("certificate changed across the restore: %s -> %s", certBefore, got)
	}
}

// leaf returns the SHA-256 of the certificate Caddy serves for host.
func (h *harness) leaf(host string) string {
	h.t.Helper()
	addr := strings.Fields(h.docker("port", h.caddy, "443/tcp"))[0]
	var last error
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", addr,
			&tls.Config{ServerName: host, InsecureSkipVerify: true}) // Caddy's internal CA
		if err != nil {
			last = err
			continue
		}
		certs := conn.ConnectionState().PeerCertificates
		conn.Close()
		if len(certs) > 0 {
			sum := sha256.Sum256(certs[0].Raw)
			return hex.EncodeToString(sum[:])
		}
	}
	h.t.Fatalf("no certificate for %s: %v", host, last)
	return ""
}
