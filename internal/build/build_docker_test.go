//go:build docker

package build

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests need Docker Engine with buildx. They run on the owner's machine
// (`make test-docker`), not in CI (CLAUDE.md §6). Each test uses its own
// builder and removes it, with its images, afterwards.

const deployment = "11111111-1111-4111-8111-111111111111"

func newTestBuilder(t *testing.T) *Builder {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is required for these tests")
	}
	b := &Builder{Name: "shipyard-test-" + strings.ToLower(rand.Text()[:8]), Timeout: 5 * time.Minute}
	if err := b.Ensure(t.Context(), Limits{Memory: "512m", CPUQuota: 100000}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		b.Remove(ctx)
	})
	return b
}

func project(t *testing.T, dockerfile string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644)
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644)
	return filepath.Join(dir, "Dockerfile"), dir
}

func dockerOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func removeImage(t *testing.T, ref string) {
	t.Cleanup(func() { exec.Command("docker", "image", "rm", "--force", ref).Run() })
}

// The builder container really carries the configured limits (ADR-0004).
func TestBuilderLimits(t *testing.T) {
	b := newTestBuilder(t)
	container := "buildx_buildkit_" + b.Name + "0"
	got := dockerOut(t, "inspect", "--format", "{{.HostConfig.Memory}} {{.HostConfig.CpuQuota}} {{.HostConfig.CpuPeriod}}", container)
	if got != "536870912 100000 100000" {
		t.Fatalf("builder limits = %q, want 512 MiB and 1 CPU", got)
	}
	// Ensure is idempotent.
	if err := b.Ensure(t.Context(), Limits{}); err != nil {
		t.Fatal(err)
	}
}

// ADR-0009: the builder's container is on its own labelled network only,
// not on Docker's default bridge, and Remove deletes that network too.
func TestBuilderNetwork(t *testing.T) {
	b := newTestBuilder(t)
	container := "buildx_buildkit_" + b.Name + "0"
	if got := dockerOut(t, "inspect", "--format", "{{.HostConfig.NetworkMode}} {{range $k, $v := .NetworkSettings.Networks}}[{{$k}}]{{end}}", container); got != b.Network()+" ["+b.Network()+"]" {
		t.Fatalf("builder networks = %q, want only %s", got, b.Network())
	}
	if got := dockerOut(t, "network", "inspect", "--format", `{{index .Labels "io.shipyard.role"}} {{.Driver}} {{.Internal}}`, b.Network()); got != "build bridge false" {
		t.Fatalf("network = %q", got)
	}
	if err := b.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if exec.Command("docker", "network", "inspect", b.Network()).Run() == nil {
		t.Fatal("Remove left the builder's network")
	}
}

func TestBuildSucceeds(t *testing.T) {
	b := newTestBuilder(t)
	dockerfile, ctxDir := project(t, "FROM scratch\nCOPY hello.txt /hello.txt\n")
	commit := strings.Repeat("ab", 20)
	var lines []string
	res, err := b.Build(t.Context(), Request{Dockerfile: dockerfile, Context: ctxDir, App: "web",
		Commit: commit, DeploymentID: deployment, Log: func(s string) { lines = append(lines, s) }})
	if err != nil {
		t.Fatal(err)
	}
	removeImage(t, res.Tag)
	if res.Tag != "shipyard/web:abababababab" || !strings.HasPrefix(res.ImageID, "sha256:") {
		t.Fatalf("result = %+v", res)
	}
	// With --load, buildx reports containerimage.digest but not always
	// containerimage.config.digest (observed with buildx 0.37.1) [DK-BX-BUILD].
	if !strings.HasPrefix(res.Digest, "sha256:") || !strings.Contains(string(res.Metadata), "containerimage.descriptor") {
		t.Fatalf("metadata = %s", res.Metadata)
	}
	// On the containerd image store the Engine image ID is the manifest digest.
	if strings.Contains(dockerOut(t, "info", "--format", "{{.DriverStatus}}"), "io.containerd.snapshotter") && res.ImageID != res.Digest {
		t.Fatalf("image ID %s differs from containerimage.digest %s", res.ImageID, res.Digest)
	}
	// Containers are created from the image ID; it must resolve to this build.
	labels := dockerOut(t, "image", "inspect", "--format",
		`{{index .Config.Labels "io.shipyard.managed"}} {{index .Config.Labels "io.shipyard.app"}} {{index .Config.Labels "io.shipyard.commit"}} {{index .Config.Labels "io.shipyard.deployment"}}`,
		res.ImageID)
	if labels != "true web "+commit+" "+deployment {
		t.Fatalf("labels = %q", labels)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "COPY hello.txt") {
		t.Fatalf("build log not captured: %q", lines)
	}
}

// P3.4b: the cache is pruned only down to the cap; buildx reports what it
// reclaimed [DK-BX-PRUNE].
func TestPruneCache(t *testing.T) {
	b := newTestBuilder(t)
	dockerfile, ctxDir := project(t, "FROM scratch\nCOPY blob /blob\n")
	blob := make([]byte, 2<<20)
	rand.Read(blob)
	if err := os.WriteFile(filepath.Join(ctxDir, "blob"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := b.Build(t.Context(), Request{Dockerfile: dockerfile, Context: ctxDir, App: "web",
		Commit: strings.Repeat("cd", 20), DeploymentID: deployment})
	if err != nil {
		t.Fatal(err)
	}
	removeImage(t, res.ImageID)
	for _, tc := range []struct {
		max  int64
		none bool
	}{{1 << 40, true}, {1, false}, {1, true}} {
		got, err := b.PruneCache(t.Context(), tc.max)
		if err != nil || got == "" || (got == "0B") != tc.none {
			t.Fatalf("PruneCache(%d) = %q, %v; want reclaimed=%v", tc.max, got, err, !tc.none)
		}
	}
	if _, err := b.PruneCache(t.Context(), 0); err == nil {
		t.Fatal("a zero cap was accepted")
	}
}

// Exit criterion: a broken Dockerfile yields a failure with a bounded log.
func TestBuildFailsWithBoundedLog(t *testing.T) {
	b := newTestBuilder(t)
	b.MaxLog = 400
	dockerfile, ctxDir := project(t, "FROM scratch\nCOPY missing-file /x\n")
	var logged int
	_, err := b.Build(t.Context(), Request{Dockerfile: dockerfile, Context: ctxDir, App: "web",
		Commit: strings.Repeat("cd", 20), DeploymentID: deployment, Log: func(s string) { logged += len(s) + 1 }})
	if !errors.Is(err, ErrBuildFailed) || !strings.Contains(err.Error(), "missing-file") {
		t.Fatalf("err = %v", err)
	}
	if logged > 400+200 { // the budget plus one truncation notice
		t.Fatalf("logged %d bytes, want at most the 400-byte budget", logged)
	}
}

func TestBuildDeadline(t *testing.T) {
	b := newTestBuilder(t)
	b.Timeout = time.Millisecond
	dockerfile, ctxDir := project(t, "FROM scratch\nCOPY hello.txt /hello.txt\n")
	_, err := b.Build(t.Context(), Request{Dockerfile: dockerfile, Context: ctxDir, App: "web",
		Commit: strings.Repeat("ef", 20), DeploymentID: deployment})
	if !errors.Is(err, ErrBuildFailed) || !strings.Contains(err.Error(), "deadline of 1ms exceeded") {
		t.Fatalf("err = %v", err)
	}
}
