//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
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

	// 1. A pinned SHA deploys; replaying the Idempotency-Key is the same operation.
	op1 := h.deploy("--ref", h.repo.good, "--idempotency-key", "e2e-1")
	if out := h.cli("", "deploy", slug, "--ref", h.repo.good, "--idempotency-key", "e2e-1"); !strings.Contains(out, "Already requested") || !strings.Contains(out, op1) {
		t.Fatalf("replay:\n%s", out)
	}
	h.wantOp(op1, "succeeded", "")
	first := h.onlyRunning()
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

	// 2. A broken Dockerfile at the branch head fails with the build log.
	h.wantOp(h.deploy(), "failed", "missing-file")
	// 3. A commit that is not on the tracked branch is refused.
	h.wantOp(h.deploy("--ref", h.repo.offBranch), "failed", "commit is not on the tracked branch")
	// 4. An unhealthy release is removed; the running one stays.
	h.cli("1\n", "env", "set", slug, "PROBE_UNHEALTHY", "--plain")
	h.wantOp(h.deploy("--ref", h.repo.good), "failed", "status 500")
	if now := h.onlyRunning(); now != first {
		t.Fatalf("a failed deploy replaced the running container: %s -> %s", first, now)
	}

	// 5. A healthy release supersedes the first, whose container is removed.
	h.cli("", "env", "unset", slug, "PROBE_UNHEALTHY")
	h.wantOp(h.deploy("--ref", h.repo.good), "succeeded", "")
	// The operation succeeds when the new release is active; the old one is
	// drained right after (SIGTERM, then removal).
	gone := false
	for deadline := time.Now().Add(30 * time.Second); !gone && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		gone = exec.Command("docker", "inspect", first).Run() != nil
	}
	if !gone {
		t.Fatal("the superseded container was not removed")
	}
	if second := h.onlyRunning(); second == first {
		t.Fatal("the second release did not replace the first")
	}
	if n := h.docker("ps", "-aq", "--filter", "label=io.shipyard.app="+slug); strings.Count(n, "\n") != 0 {
		t.Fatalf("leftover containers of %s:\n%s", slug, n)
	}
}

type harness struct {
	t      *testing.T
	bin    string
	slug   string
	caddy  string // the worker's edge container
	repo   repo
	env    []string // for the CLI
	ops    []string // every operation deployed, for dumpEvents
	opRE   *regexp.Regexp
	status *regexp.Regexp
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
		out, _ = exec.Command("docker", "image", "ls", "-q", "shipyard/"+h.slug).Output()
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
	t.Cleanup(func() { h.dumpEvents(dbURL) }) // before the database is dropped
	common := append(cleanEnv(), "SHIPYARD_DATABASE_URL="+dbURL,
		"SHIPYARD_KEK_DIR="+kek, "SHIPYARD_KEK_ACTIVE=e2e", "SHIPYARD_LOG_FORMAT=text")
	run(t, tmp, common, h.bin+"/shipyard-api", "migrate")
	token := run(t, tmp, common, h.bin+"/shipyard-api", "token", "create", "--name", "e2e")

	addr := freeAddr(t)
	h.spawn("api", append(common, "SHIPYARD_API_LISTEN="+addr), "shipyard-api", "serve")
	waitHTTP(t, "http://"+addr+"/readyz")
	h.spawn("worker", append(common, "SHIPYARD_WORK_DIR="+filepath.Join(tmp, "work"), "SHIPYARD_SOURCE_BASE_URL="+gitURL,
		"SHIPYARD_BUILDER="+builder, "SHIPYARD_BUILDER_MEMORY=1g", "SHIPYARD_BUILDER_CPUS=1",
		"SHIPYARD_CADDY_NAME="+h.caddy, "SHIPYARD_CADDY_ADMIN_DIR="+filepath.Join(tmp, "caddy"),
		"SHIPYARD_CADDY_BIND=127.0.0.1", "SHIPYARD_CADDY_HTTP_PORT=0", "SHIPYARD_CADDY_HTTPS_PORT=0", "SHIPYARD_CADDY_CA=internal",
		"SHIPYARD_WORKER_POLL_INTERVAL=200ms"), "shipyard-worker", "run")
	h.env = append(cleanEnv(), "SHIPYARD_URL=http://"+addr, "SHIPYARD_TOKEN="+token,
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

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
	}
	t.Fatalf("%s did not become ready", url)
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
