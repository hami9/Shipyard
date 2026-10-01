//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
)

// The Phase 1 exit criteria (docs/ROADMAP.md), end to end: app create, env
// set, and deploy run a hardened, healthy container from the pinned SHA; a
// broken Dockerfile, a SHA off the branch, and an unhealthy release fail
// without touching the running one; an Idempotency-Key replay is one deploy.
func TestPhase1ExitCriteria(t *testing.T) {
	h := start(t)
	slug := h.slug

	h.cli("", "app", "create", slug, "--repo", "e2e/demo", "--branch", "main", "--port", "8080", "--health-path", "/healthz")
	h.cli("hello from e2e\n", "env", "set", slug, "GREETING")
	// P2.4/P2.5: a hostname for the app, through the domain API (the DNS
	// preflight is off: the e2e hostnames have no public DNS).
	const host, extra = "app.e2e.example", "extra.e2e.example"
	h.cli("", "domain", "add", slug, host)

	// 1. A pinned SHA deploys; replaying the Idempotency-Key is the same operation.
	op1 := h.deploy("--ref", h.repo.good, "--idempotency-key", "e2e-1")
	if out := h.cli("", "deploy", slug, "--ref", h.repo.good, "--idempotency-key", "e2e-1"); !strings.Contains(out, "Already requested") || !strings.Contains(out, op1) {
		t.Fatalf("replay:\n%s", out)
	}
	h.wantOp(op1, "succeeded", "")
	first := h.onlyRunning()
	firstImage := h.docker("inspect", "--format", "{{.Image}}", first)
	if got := h.docker("inspect", "--format", `{{index .Config.Labels "io.shipyard.commit"}}`, first); got != h.repo.good {
		t.Fatalf("running commit %s, want the pinned %s", got, h.repo.good)
	}
	got := h.docker("inspect", "--format", "{{json .HostConfig.CapDrop}} {{json .HostConfig.SecurityOpt}} {{.HostConfig.LogConfig.Type}} "+
		"{{.HostConfig.Memory}} {{.HostConfig.NanoCpus}} {{json .HostConfig.PidsLimit}} {{.HostConfig.Privileged}} {{json .HostConfig.PortBindings}}", first)
	if want := `["ALL"] ["no-new-privileges"] local 536870912 1000000000 512 false {}`; got != want {
		t.Fatalf("hardening = %s\nwant        %s", got, want)
	}
	ip := h.docker("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", first)
	if v := get(t, "http://"+ip+":8080/env?key=GREETING"); v != "hello from e2e" {
		t.Fatalf("GREETING = %q", v)
	}
	// P2.1: the worker's Caddy is the only container publishing ports, and
	// it joined the app's network.
	if nets := h.docker("inspect", "--format", "{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}", h.caddy); !strings.Contains(nets, "shipyard-app-"+slug) {
		t.Fatalf("caddy networks = %q", nets)
	}
	if ports := h.docker("port", first); ports != "" {
		t.Fatalf("the app container publishes ports: %s", ports)
	}
	// P2.4: HTTPS through Caddy reaches this container (its hostname is its
	// short ID) and carries the injected environment.
	if got := h.viaCaddy(host, "/read?path=/etc/hostname"); got != first[:12] {
		t.Fatalf("caddy serves %q, want the first container %s", got, first[:12])
	}
	if got := h.viaCaddy(host, "/env?key=GREETING"); got != "hello from e2e" {
		t.Fatalf("GREETING through caddy = %q", got)
	}
	// P2.5: a hostname added while the app serves targets the running
	// container at once; the worker's reconciler loads it into Caddy.
	if out := h.cli("", "domain", "add", slug, extra); !strings.Contains(out, "serving deployment") {
		t.Fatalf("domain add:\n%s", out)
	}
	if got := h.viaCaddy(extra, "/read?path=/etc/hostname"); got != first[:12] {
		t.Fatalf("%s through caddy = %q, want %s", extra, got, first[:12])
	}
	// P2.8: Caddy publishes the API (listening on a Unix socket only) on its
	// own hostname over HTTPS with HTTP/2; only /v1/* and /hooks/github.
	resp, body := h.apiViaCaddy("/v1/whoami")
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 2 || !strings.Contains(body, `"e2e"`) {
		t.Fatalf("whoami through caddy: %s %s %s", resp.Proto, resp.Status, body)
	}
	if resp, body := h.apiViaCaddy("/readyz"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/readyz through caddy: %s %s", resp.Status, body)
	}
	// SSE through Caddy: the finished deploy's events stream to their end.
	resp, body = h.apiViaCaddy("/v1/operations/" + op1 + "/events")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" ||
		!strings.Contains(body, "is active") || !strings.Contains(body, "event: end") {
		t.Fatalf("events through caddy: %s %s", resp.Status, body)
	}
	// Apps cannot take the API's hostname.
	if out, err := h.cliErr("domain", "add", slug, apiHost); err == nil || !strings.Contains(out, "reserved for the Shipyard API") {
		t.Fatalf("domain add %s: %v\n%s", apiHost, err, out)
	}

	// 2. A broken Dockerfile at the branch head fails with the build log.
	h.wantOp(h.deploy(), "failed", "missing-file")
	// 3. A commit that is not on the tracked branch is refused.
	h.wantOp(h.deploy("--ref", h.repo.offBranch), "failed", "commit is not on the tracked branch")
	// 4. An unhealthy release is removed; the running one stays. Phase 2 exit
	// criterion: a client probing throughout sees only 2xx, all from the
	// running release.
	h.cli("1\n", "env", "set", slug, "PROBE_UNHEALTHY", "--plain")
	p := h.probe(host)
	h.wantOp(h.deploy("--ref", h.repo.good), "failed", "status 500")
	if got := p.stop(); got.bad != 0 || got.served[first[:12]] != got.total || got.total < 10 {
		t.Fatalf("probing through the unhealthy deploy: %+v, want every answer 2xx from %s", got, first[:12])
	}
	if now := h.onlyRunning(); now != first {
		t.Fatalf("a failed deploy replaced the running container: %s -> %s", first, now)
	}
	// 4b. Phase 2 exit criterion: a candidate that passes its health gate but
	// fails verification through Caddy (injected: it fails /healthz by
	// hostname only) is rolled back; Caddy goes back to the committed routes.
	h.cli("", "env", "unset", slug, "PROBE_UNHEALTHY")
	h.cli("1\n", "env", "set", slug, "PROBE_FAIL_BY_NAME", "--plain")
	p = h.probe(host)
	h.wantOp(h.deploy("--ref", h.repo.good), "failed", "switch traffic")
	sw := p.stop()
	// The load puts traffic on the candidate until the restore (ARCHITECTURE
	// §5 failure table), so only 2xx is required here, not the origin.
	if sw.bad != 0 || sw.total < 10 {
		t.Fatalf("probing through the failed switch: %+v", sw)
	}
	t.Logf("failed switch: %d probes, %d answered by the candidate before the restore", sw.total, sw.total-sw.served[first[:12]])
	if now := h.onlyRunning(); now != first {
		t.Fatalf("a failed switch replaced the running container: %s -> %s", first, now)
	}
	// The whole committed route table is back, every hostname of the app.
	for _, hn := range []string{host, extra} {
		if got := h.viaCaddy(hn, "/read?path=/etc/hostname"); got != first[:12] {
			t.Fatalf("after failed deploys caddy serves %q on %s, want %s", got, hn, first[:12])
		}
	}

	// 5. A healthy release supersedes the first, whose container is removed.
	h.cli("", "env", "unset", slug, "PROBE_FAIL_BY_NAME")
	// P2.7: --follow streams the events (SSE) to the end, including the one
	// the worker appends after the operation has finished (the drain plan).
	out := h.cli("", "deploy", slug, "--ref", h.repo.good, "--follow")
	if m := h.opRE.FindStringSubmatch(out); m != nil {
		h.ops = append(h.ops, m[1])
	}
	for _, want := range []string{"health check: GET /healthz", "verified through caddy: ", "is active", "keeps running through the observation window", "succeeded"} {
		if !strings.Contains(out, want) {
			t.Fatalf("deploy --follow lacks %q:\n%s", want, out)
		}
	}
	// P2.6: the operation succeeds when the new release is active; the old
	// one keeps running through the observation window (6s here), then the
	// reconciler stops it gracefully and removes it.
	if got := h.docker("inspect", "--format", "{{.State.Running}}", first); got != "true" {
		t.Fatalf("the superseded container stopped before its observation window: running=%s", got)
	}
	gone := false
	for deadline := time.Now().Add(30 * time.Second); !gone && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		gone = exec.Command("docker", "inspect", first).Run() != nil
	}
	if !gone {
		t.Fatal("the superseded container was not removed")
	}
	second := h.onlyRunning()
	if second == first {
		t.Fatal("the second release did not replace the first")
	}
	for _, hn := range []string{host, extra} {
		if got := h.viaCaddy(hn, "/read?path=/etc/hostname"); got != second[:12] {
			t.Fatalf("after the switch caddy serves %q on %s, want the second container %s", got, hn, second[:12])
		}
	}
	// P3.4: image retention (1 earlier release here) removes the images of
	// the failed deploys of steps 4 and 4b; the active release's and the
	// superseded first release's (the rollback target) stay.
	secondImage := h.docker("inspect", "--format", "{{.Image}}", second)
	h.waitImages(firstImage, secondImage)
	// P3.4b: only the 2 newest operations keep their events; the first
	// deploy's are removed, while its operation row stays.
	h.waitEvents(op1, 0)
	if n := h.events(h.ops[len(h.ops)-1]); n == 0 {
		t.Fatal("the newest operation lost its events")
	}
	h.wantOp(op1, "succeeded", "")
	// P3.2: the reconciler brings the active release back. A container that
	// is gone is recreated from the deployment's image and environment under
	// the same name, so Caddy reaches it without a config change; a stopped
	// one is started.
	name := h.docker("inspect", "--format", "{{.Name}}", second)
	h.docker("rm", "--force", second)
	third := h.waitRunning(func(id string) bool { return id != second })
	if got := h.docker("inspect", "--format", "{{.Name}}", third); got != name {
		t.Fatalf("recreated as %s, want the same name %s", got, name)
	}
	if got := h.viaCaddy(host, "/env?key=GREETING"); got != "hello from e2e" {
		t.Fatalf("recreated container: GREETING through caddy = %q", got)
	}
	if out, err := exec.Command("docker", "stop", "--timeout", "1", third).CombinedOutput(); err != nil {
		state, _ := exec.Command("docker", "inspect", "--format", "{{json .State}} restarts={{.RestartCount}}", third).CombinedOutput()
		logs, _ := exec.Command("docker", "logs", "--tail", "20", third).CombinedOutput()
		t.Fatalf("docker stop %s: %v\n%s\nstate: %s\nlogs:\n%s", third[:12], err, out, state, logs)
	}
	if got := h.waitRunning(func(string) bool { return true }); got != third {
		t.Fatalf("a stopped active container came back as %s, want %s started again", got, third)
	}
	if got := h.viaCaddy(host, "/read?path=/etc/hostname"); got != third[:12] {
		t.Fatalf("after the restore caddy serves %q, want %s", got, third[:12])
	}
	// P3.1: the history, newest first: the active release, the two failed
	// deploys of steps 4 and 4b, the failed build of step 2 (the off-branch
	// commit of step 3 was refused before a deployment existed), and the
	// superseded first release. Each pins its environment revision.
	rel := h.cli("", "releases", slug)
	var statuses, commits []string
	for _, line := range strings.Split(strings.TrimSpace(rel), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasPrefix(f[3], "#") {
			t.Fatalf("release row %q (no env revision):\n%s", line, rel)
		}
		statuses, commits = append(statuses, f[1]), append(commits, f[2])
	}
	if got := strings.Join(statuses, " "); got != "active failed failed failed superseded" || commits[0] != h.repo.good[:12] {
		t.Fatalf("releases = %s (active commit %s, want %s):\n%s", got, commits[0], h.repo.good[:12], rel)
	}

	// P2.7b: logs come from the worker's socket through the API (ADR-0008),
	// with the secret GREETING redacted; stderr lines are there too.
	h.viaCaddy(host, "/say?text=greeting+is+hello+from+e2e")
	logs := h.cli("", "logs", slug, "--tail", "20")
	if !strings.Contains(logs, "greeting is [REDACTED]") || strings.Contains(logs, "hello from e2e") || !strings.Contains(logs, "probe listening on :8080") {
		t.Fatalf("logs:\n%s", logs)
	}

	// P3.3: roll back to the first release (the superseded row of the
	// history). GREETING changed since, so a plain rollback is refused and
	// names the key; with its old configuration, the first release's image
	// runs again with the old value, through the health gate and the switch.
	var firstDep string
	for _, line := range strings.Split(h.cli("", "releases", slug), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[1] == "superseded" {
			firstDep = f[0]
		}
	}
	h.cli("rotated value\n", "env", "set", slug, "GREETING")
	if out, err := h.cliErr("rollback", slug, "--to", firstDep); err == nil || !strings.Contains(out, "GREETING") || strings.Contains(out, "hello from e2e") {
		t.Fatalf("rollback with a rotated secret: %v\n%s", err, out)
	}
	out = h.cli("", "rollback", slug, "--to", firstDep, "--with-old-config", "--follow")
	if m := h.opRE.FindStringSubmatch(out); m != nil {
		h.ops = append(h.ops, m[1])
	}
	if !strings.Contains(out, "rolling back to deployment "+firstDep) || !strings.Contains(out, "succeeded") {
		t.Fatalf("rollback --follow:\n%s", out)
	}
	rolled := h.waitRunning(func(id string) bool { return id != third }) // the old one drains
	if got := h.viaCaddy(host, "/env?key=GREETING"); got != "hello from e2e" {
		t.Fatalf("after the rollback GREETING = %q, want the old value", got)
	}
	if got := h.viaCaddy(host, "/read?path=/etc/hostname"); got != rolled[:12] {
		t.Fatalf("after the rollback caddy serves %q, want %s", got, rolled[:12])
	}
	if rel := h.cli("", "releases", slug, "--limit", "1"); !strings.Contains(rel, "active") || !strings.Contains(rel, "rollback of "+firstDep) {
		t.Fatalf("history after the rollback:\n%s", rel)
	}
	// The rollback runs the first image again; the second release is now
	// the one earlier release retention keeps.
	h.waitImages(firstImage, secondImage)
	// A removed hostname leaves Caddy at the next sync: no route, no certificate.
	h.cli("", "domain", "remove", slug, extra)
	h.gone(extra)
	if n := h.docker("ps", "-aq", "--filter", "label=io.shipyard.app="+slug); strings.Count(n, "\n") != 0 {
		t.Fatalf("leftover containers of %s:\n%s", slug, n)
	}
	h.checkBackup()
}

// checkBackup runs `shipyard-worker backup` beside the running worker
// (P3.5): target A gets a pg_dump archive that pg_restore can read, Caddy's
// data with its CA, and a manifest; target B gets the KEK; the hook sees the
// new backup. pg_dump and pg_restore come from the postgres:18 image, as the
// test host has no PostgreSQL client.
func (h *harness) checkBackup() {
	t := h.t
	t.Helper()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	pgDump := filepath.Join(dir, "pg_dump")
	script := "#!/bin/sh\nexec docker run --rm -i --network host -e PGPASSWORD postgres:18 pg_dump \"$@\"\n"
	if err := os.WriteFile(pgDump, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out := run(t, dir, append(slices.Clone(h.workerEnv), "SHIPYARD_BACKUP_DIR="+a, "SHIPYARD_BACKUP_KEK_DIR="+b,
		"SHIPYARD_BACKUP_PG_DUMP="+pgDump, "SHIPYARD_BACKUP_HOOK=ls \"$SHIPYARD_BACKUP_PATH\" > "+filepath.Join(dir, "hook")),
		h.bin+"/shipyard-worker", "backup")
	backups, _ := os.ReadDir(a)
	if len(backups) != 1 {
		t.Fatalf("target A has %d backups:\n%s", len(backups), out)
	}
	one := filepath.Join(a, backups[0].Name())
	dump, err := os.ReadFile(filepath.Join(one, "database.dump"))
	if err != nil || !bytes.HasPrefix(dump, []byte("PGDMP")) {
		t.Fatalf("database.dump: %v, starts %q", err, dump[:min(len(dump), 8)])
	}
	list := exec.Command("docker", "run", "--rm", "-i", "postgres:18", "pg_restore", "--list")
	list.Stdin = bytes.NewReader(dump)
	toc, err := list.CombinedOutput()
	for _, table := range []string{"deployments", "secret_values", "routes", "operation_events"} {
		if err != nil || !bytes.Contains(toc, []byte("TABLE DATA public "+table+" ")) {
			t.Fatalf("pg_restore --list lacks %s: %v\n%s", table, err, toc)
		}
	}
	caddy := run(t, one, nil, "tar", "-tzf", "caddy-data.tar.gz")
	if !strings.Contains(caddy, "data/caddy/pki/authorities/local/root.crt") {
		t.Fatalf("caddy data lacks the local CA:\n%s", caddy)
	}
	manifest, _ := os.ReadFile(filepath.Join(one, "manifest.json"))
	if !bytes.Contains(manifest, []byte(`"e2e"`)) || !bytes.Contains(manifest, []byte(`"database.dump"`)) {
		t.Fatalf("manifest:\n%s", manifest)
	}
	want, _ := os.ReadFile(filepath.Join(h.kek, "e2e.key"))
	if got, err := os.ReadFile(filepath.Join(b, "e2e.key")); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("target B: %v", err)
	}
	if hook, _ := os.ReadFile(filepath.Join(dir, "hook")); !bytes.Contains(hook, []byte("manifest.json")) {
		t.Fatalf("the hook saw %q", hook)
	}
}

// apiHost is the name Caddy publishes the API on (P2.8).
const apiHost = "api.e2e.example"

type harness struct {
	t      *testing.T
	token  string // the CLI's API token
	bin    string
	slug   string
	caddy  string // the worker's edge container
	dbURL  string
	repo   repo
	env    []string // for the CLI
	ops    []string // every operation deployed, for dumpEvents
	opRE   *regexp.Regexp
	status *regexp.Regexp

	// workerEnv is the running worker's environment, for `shipyard-worker
	// backup`; kek is its KEK directory.
	workerEnv []string
	kek       string
}

type repo struct{ good, offBranch string }

func start(t *testing.T) *harness {
	t.Helper()
	root, _ := filepath.Abs("../..")
	tmp := t.TempDir()
	h := &harness{t: t, bin: filepath.Join(tmp, "bin"), slug: "e2e-" + strings.ToLower(rand.Text()[:8]),
		opRE:   regexp.MustCompile(`operation: ([0-9a-f-]{36})`),
		status: regexp.MustCompile(`(?m)^status\s+(\S+)`)}
	builder := "shipyard-e2e-" + strings.ToLower(rand.Text()[:8])
	h.caddy = builder + "-caddy"

	// Docker cleanup runs after the processes stop (cleanups run last first).
	t.Cleanup(func() {
		exec.Command("docker", "rm", "--force", h.caddy).Run() // first: it holds the app network
		exec.Command("docker", "network", "rm", h.caddy).Run()
		exec.Command("docker", "volume", "rm", h.caddy+"-data", h.caddy+"-config").Run()
		out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=io.shipyard.app="+h.slug).Output()
		for _, id := range strings.Fields(string(out)) {
			exec.Command("docker", "rm", "--force", "--volumes", id).Run()
		}
		exec.Command("docker", "network", "rm", "shipyard-app-"+h.slug).Run()
		// By label: a rebuild of the same commit leaves the previous image
		// untagged, out of reach of a name filter.
		out, _ = exec.Command("docker", "image", "ls", "-a", "-q", "--no-trunc", "--filter", "label=io.shipyard.app="+h.slug).Output()
		for _, id := range strings.Fields(string(out)) {
			exec.Command("docker", "image", "rm", "--force", id).Run()
		}
		exec.Command("docker", "buildx", "rm", "--force", builder).Run()
	})

	run(t, root, nil, "go", "build", "-o", h.bin+string(filepath.Separator), "./cmd/...")
	src := filepath.Join(tmp, "src")
	os.MkdirAll(src, 0o755)
	run(t, root, []string{"CGO_ENABLED=0", "GOOS=linux"}, "go", "build", "-o", filepath.Join(src, "probe"), "./internal/runtime/testdata/probe")
	h.repo = newRepo(t, src)
	gitURL := serveGit(t, src, filepath.Join(tmp, "repos"))

	kek := filepath.Join(tmp, "kek")
	os.Mkdir(kek, 0o700)
	key := make([]byte, 32)
	rand.Read(key)
	if err := os.WriteFile(filepath.Join(kek, "e2e.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	dbURL := storetest.NewDatabase(t)
	h.dbURL = dbURL
	t.Cleanup(func() { h.dumpEvents(dbURL) }) // before the database is dropped
	// P2.8: the API listens on a Unix socket in a directory of its own, which
	// the worker mounts into Caddy; Caddy runs with this process's group.
	apiDir := filepath.Join(tmp, "api")
	if err := os.Mkdir(apiDir, 0o750); err != nil {
		t.Fatal(err)
	}
	apiSock := filepath.Join(apiDir, "api.sock")
	common := append(cleanEnv(), "SHIPYARD_DATABASE_URL="+dbURL,
		"SHIPYARD_KEK_DIR="+kek, "SHIPYARD_KEK_ACTIVE=e2e", "SHIPYARD_LOG_FORMAT=text",
		"SHIPYARD_WORKER_SOCKET="+filepath.Join(tmp, "logs.sock"),
		"SHIPYARD_API_LISTEN=unix:"+apiSock, "SHIPYARD_API_HOSTNAME="+apiHost)
	run(t, tmp, common, h.bin+"/shipyard-api", "migrate")
	token := run(t, tmp, common, h.bin+"/shipyard-api", "token", "create", "--name", "e2e")
	h.token = token

	h.spawn("api", append(common, "SHIPYARD_DNS_PREFLIGHT=false"), "shipyard-api", "serve")
	waitUnix(t, apiSock)
	h.kek = kek
	h.workerEnv = append(common, "SHIPYARD_WORK_DIR="+filepath.Join(tmp, "work"), "SHIPYARD_SOURCE_BASE_URL="+gitURL,
		"SHIPYARD_BUILDER="+builder, "SHIPYARD_BUILDER_MEMORY=1g", "SHIPYARD_BUILDER_CPUS=1",
		"SHIPYARD_CADDY_NAME="+h.caddy, "SHIPYARD_CADDY_ADMIN_DIR="+filepath.Join(tmp, "caddy"),
		"SHIPYARD_CADDY_BIND=127.0.0.1", "SHIPYARD_CADDY_HTTP_PORT=0", "SHIPYARD_CADDY_HTTPS_PORT=0", "SHIPYARD_CADDY_CA=internal",
		"SHIPYARD_WORKER_POLL_INTERVAL=200ms", "SHIPYARD_RECONCILE_INTERVAL=1s", "SHIPYARD_OBSERVATION_WINDOW=6s",
		"SHIPYARD_RETAIN_IMAGES=1", "SHIPYARD_RETENTION_INTERVAL=3s", "SHIPYARD_RETAIN_OPERATIONS=2")
	h.spawn("worker", h.workerEnv, "shipyard-worker", "run")
	h.env = append(cleanEnv(), "SHIPYARD_URL=unix://"+apiSock, "SHIPYARD_TOKEN="+token,
		"SHIPYARD_CONFIG="+filepath.Join(tmp, "cli.json"))
	return h
}

// newRepo commits a working app, then a feature branch commit, then a broken
// Dockerfile at the head of main.
func newRepo(t *testing.T, src string) repo {
	git := func(args ...string) string {
		return run(t, src, []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=" + os.DevNull}, "git", args...)
	}
	commit := func(file, content string) string {
		os.WriteFile(filepath.Join(src, file), []byte(content), 0o644)
		git("add", "-A")
		git("commit", "-q", "-m", file)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	var r repo
	r.good = commit("Dockerfile", "FROM scratch\nCOPY probe /probe\nEXPOSE 8080\nENTRYPOINT [\"/probe\"]\n")
	git("switch", "-q", "-c", "feature")
	r.offBranch = commit("feature.txt", "not on main\n")
	git("switch", "-q", "main")
	commit("Dockerfile", "FROM scratch\nCOPY missing-file /x\n")
	return r
}

// serveGit publishes src as e2e/demo over git's smart HTTP protocol.
func serveGit(t *testing.T, src, root string) string {
	bare := filepath.Join(root, "e2e", "demo.git")
	os.MkdirAll(filepath.Dir(bare), 0o755)
	run(t, root, nil, "git", "clone", "-q", "--bare", src, bare)
	run(t, bare, nil, "git", "config", "uploadpack.allowFilter", "true")
	backend := &cgi.Handler{Path: filepath.Join(run(t, root, nil, "git", "--exec-path"), "git-http-backend"),
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)
	return srv.URL
}

// spawn starts a binary and stops it with SIGTERM when the test ends. Its
// output is printed if the test fails.
func (h *harness) spawn(name string, env []string, bin string, args ...string) {
	var out bytes.Buffer
	cmd := exec.Command(filepath.Join(h.bin, bin), args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, &out, &out
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		if h.t.Failed() {
			h.t.Logf("--- %s output:\n%s", name, out.String())
		}
	})
}

func (h *harness) cli(stdin string, args ...string) string {
	h.t.Helper()
	cmd := exec.Command(filepath.Join(h.bin, "shipyard"), args...)
	cmd.Env, cmd.Stdin = h.env, strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("shipyard %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// probeResult counts what a continuous client saw: answers by container
// (its hostname is the short container ID), and failures of any kind.
type probeResult struct {
	total, bad int
	served     map[string]int
	failures   []string // the first few
}

type prober struct {
	stopc chan struct{}
	done  chan probeResult
}

// probe requests host through Caddy's HTTPS listener every 50 ms, one
// attempt each and no retries, until stop: a dropped connection or a non-2xx
// answer is downtime.
func (h *harness) probe(host string) *prober {
	h.t.Helper()
	addr := strings.Fields(h.docker("port", h.caddy, "443/tcp"))[0]
	hc := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Caddy's internal CA
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}}}
	p := &prober{stopc: make(chan struct{}), done: make(chan probeResult, 1)}
	go func() {
		r := probeResult{served: map[string]int{}}
		fail := func(s string) {
			r.bad++
			if len(r.failures) < 5 {
				r.failures = append(r.failures, s)
			}
		}
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-p.stopc:
				p.done <- r
				return
			case <-tick.C:
			}
			r.total++
			resp, err := hc.Get("https://" + host + "/read?path=/etc/hostname")
			if err != nil {
				fail(err.Error())
				continue
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				fail(resp.Status + ": " + string(b))
				continue
			}
			r.served[strings.TrimSpace(string(b))]++
		}
	}()
	return p
}

func (p *prober) stop() probeResult {
	close(p.stopc)
	return <-p.done
}

// cliErr runs a CLI command that is expected to fail.
func (h *harness) cliErr(args ...string) (string, error) {
	cmd := exec.Command(filepath.Join(h.bin, "shipyard"), args...)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *harness) deploy(args ...string) string {
	h.t.Helper()
	out := h.cli("", append([]string{"deploy", h.slug}, args...)...)
	m := h.opRE.FindStringSubmatch(out)
	if m == nil {
		h.t.Fatalf("deploy output:\n%s", out)
	}
	h.ops = append(h.ops, m[1])
	return m[1]
}

// gone waits until Caddy no longer routes host to the app: the TLS handshake
// fails, or a request no longer reaches the probe (Caddy may keep a cached
// certificate; an unmatched request gets its empty default response).
func (h *harness) gone(host string) {
	h.t.Helper()
	addr := strings.Fields(h.docker("port", h.caddy, "443/tcp"))[0]
	hc := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}}}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		resp, err := hc.Get("https://" + host + "/env?key=GREETING")
		if err != nil {
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != "hello from e2e" {
			return
		}
	}
	h.t.Fatalf("caddy still routes %s to the app", host)
}

// viaCaddy GETs https://host/path through Caddy's published port. The e2e
// Caddy uses its internal CA, so the certificate is not verified here.
func (h *harness) viaCaddy(host, path string) string {
	h.t.Helper()
	out := h.docker("port", h.caddy, "443/tcp")
	addr := strings.Fields(out)[0]
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}}}
	var last error
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		resp, err := hc.Get("https://" + host + path)
		if err != nil {
			last = err
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("%s: %s", resp.Status, b)
			continue
		}
		return strings.TrimSpace(string(b))
	}
	h.t.Fatalf("https://%s%s through caddy: %v", host, path, last)
	return ""
}

// dumpEvents prints every operation's event log when the test failed.
func (h *harness) dumpEvents(dbURL string) {
	if !h.t.Failed() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := store.Open(ctx, dbURL)
	if err != nil {
		h.t.Logf("events unavailable: %v", err)
		return
	}
	defer pool.Close()
	s := store.New(pool)
	for _, id := range h.ops {
		events, _ := s.OperationEvents(ctx, id, 0, 1000)
		var b strings.Builder
		for _, e := range events {
			fmt.Fprintf(&b, "%s %s\n", e.Level, e.Message)
		}
		h.t.Logf("--- events of operation %s:\n%s", id, b.String())
	}
}

// events counts an operation's stored events.
func (h *harness) events(id string) int {
	h.t.Helper()
	pool, err := store.Open(h.t.Context(), h.dbURL)
	if err != nil {
		h.t.Fatal(err)
	}
	defer pool.Close()
	events, err := store.New(pool).OperationEvents(h.t.Context(), id, 0, 10000)
	if err != nil {
		h.t.Fatal(err)
	}
	return len(events)
}

// waitEvents waits up to 30 s until the operation has want events.
func (h *harness) waitEvents(id string, want int) {
	h.t.Helper()
	n := -1
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if n = h.events(id); n == want {
			return
		}
	}
	h.t.Fatalf("operation %s has %d events, want %d", id, n, want)
}

// wantOp waits for the operation to finish and checks its outcome.
func (h *harness) wantOp(id, status, errPart string) {
	h.t.Helper()
	var out string
	for deadline := time.Now().Add(5 * time.Minute); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		out = h.cli("", "operation", id)
		m := h.status.FindStringSubmatch(out)
		if m == nil || m[1] == "queued" || m[1] == "running" {
			continue
		}
		if m[1] != status || !strings.Contains(out, errPart) {
			h.t.Fatalf("operation %s: want %s containing %q, got:\n%s", id, status, errPart, out)
		}
		return
	}
	h.t.Fatalf("operation %s did not finish:\n%s", id, out)
}

// waitRunning waits up to 30 s until the app has exactly one running
// container that ok accepts, and returns it.
func (h *harness) waitRunning(ok func(id string) bool) string {
	h.t.Helper()
	var ids []string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		ids = strings.Fields(h.docker("ps", "-q", "--no-trunc", "--filter", "label=io.shipyard.app="+h.slug))
		if len(ids) == 1 && ok(ids[0]) {
			return ids[0]
		}
	}
	h.t.Fatalf("running containers of %s: %v", h.slug, ids)
	return ""
}

// waitImages waits until the app's images on the host are exactly want.
func (h *harness) waitImages(want ...string) {
	h.t.Helper()
	slices.Sort(want)
	var got []string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		got = strings.Fields(h.docker("image", "ls", "-a", "-q", "--no-trunc", "--filter", "label=io.shipyard.app="+h.slug))
		slices.Sort(got)
		if got = slices.Compact(got); slices.Equal(got, want) {
			return
		}
	}
	h.t.Fatalf("images of %s: %v, want %v", h.slug, got, want)
}

// onlyRunning returns the app's one running container.
func (h *harness) onlyRunning() string {
	h.t.Helper()
	ids := strings.Fields(h.docker("ps", "-q", "--no-trunc", "--filter", "label=io.shipyard.app="+h.slug))
	if len(ids) != 1 {
		h.t.Fatalf("running containers of %s: %v, want exactly one", h.slug, ids)
	}
	return ids[0]
}

func (h *harness) docker(args ...string) string {
	h.t.Helper()
	return run(h.t, "", nil, "docker", args...)
}

func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s%s", name, strings.Join(args, " "), err, out, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// cleanEnv is the test's environment without SHIPYARD_* variables, so a
// developer's own settings never leak into the processes under test.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SHIPYARD_") {
			env = append(env, kv)
		}
	}
	return env
}

// waitUnix waits until the API answers /readyz on its socket.
func waitUnix(t *testing.T, sock string) {
	t.Helper()
	hc := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if resp, err := hc.Get("http://api/readyz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
	}
	t.Fatalf("the API on %s did not become ready", sock)
}

// apiViaCaddy calls the API through Caddy's HTTPS listener by its hostname,
// over HTTP/2 when Caddy offers it; it returns the response and its body.
func (h *harness) apiViaCaddy(path string) (*http.Response, string) {
	h.t.Helper()
	addr := strings.Fields(h.docker("port", h.caddy, "443/tcp"))[0]
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // Caddy's internal CA
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}}}
	var last error
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		req, _ := http.NewRequestWithContext(h.t.Context(), "GET", "https://"+apiHost+path, nil)
		req.Header.Set("Authorization", "Bearer "+h.token)
		resp, err := hc.Do(req)
		if err != nil {
			last = err
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusBadGateway { // the socket not yet reachable
			last = fmt.Errorf("%s: %s", resp.Status, b)
			continue
		}
		return resp, string(b)
	}
	h.t.Fatalf("https://%s%s through caddy: %v", apiHost, path, last)
	return nil, ""
}

func get(t *testing.T, url string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s: %s", url, resp.Status, b)
	}
	return string(b)
}
