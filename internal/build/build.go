// Package build turns a verified checkout into a local image with BuildKit
// (ARCHITECTURE §5 step 4, ADR-0004): a dedicated, resource-limited buildx
// builder, a hard deadline, bounded log capture, and the Engine image ID as
// the release identity.
package build

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Defaults from ADR-0004.
const (
	DefaultBuilder = "shipyard"
	DefaultTimeout = 15 * time.Minute
	DefaultMaxLog  = 5 << 20
)

// ErrBuildFailed wraps a failed build; the error text ends with the last
// lines of build output, so an operator sees why without reading all logs.
var ErrBuildFailed = errors.New("image build failed")

var (
	slugRE    = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$`)
	shaRE     = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	uuidRE    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	imageIDRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Limits caps the builder's own container (docker-container driver).
type Limits struct {
	Memory   string // e.g. "2g"
	CPUQuota int    // microseconds per 100ms period, e.g. 200000 = 2 CPUs
}

// Builder runs one build at a time on a dedicated buildx builder.
type Builder struct {
	Docker  string        // docker CLI; "docker" when empty
	Name    string        // buildx builder name; DefaultBuilder when empty
	Timeout time.Duration // per build; DefaultTimeout when zero
	MaxLog  int           // bytes of build output kept; DefaultMaxLog when zero

	once sync.Once
	slot chan struct{}
}

// Request is one build. Dockerfile and Context are absolute paths already
// resolved inside the checkout (source.Checkout.Path).
type Request struct {
	Dockerfile   string
	Context      string
	App          string // slug
	Commit       string // full SHA
	DeploymentID string
	// Log receives build output line by line, at most MaxLog bytes in total.
	Log func(line string)
}

// Result identifies the built image.
type Result struct {
	ImageID      string          // what containers are created from (ADR-0004)
	Tag          string          // shipyard/<slug>:<sha12>, for humans
	Digest       string          // containerimage.digest, when reported
	ConfigDigest string          // containerimage.config.digest, when reported
	Metadata     json.RawMessage // the full --metadata-file output, for provenance
}

func (b *Builder) docker() string {
	if b.Docker != "" {
		return b.Docker
	}
	return "docker"
}

func (b *Builder) name() string {
	if b.Name != "" {
		return b.Name
	}
	return DefaultBuilder
}

// Ensure creates the builder if it is missing and starts it. Limits apply
// only when the builder is created; changing them means removing it first.
func (b *Builder) Ensure(ctx context.Context, l Limits) error {
	if _, err := b.run(ctx, "buildx", "inspect", b.name()); err != nil {
		args := []string{"buildx", "create", "--name", b.name(), "--driver", "docker-container"}
		var opts []string
		if l.Memory != "" {
			opts = append(opts, "memory="+l.Memory)
		}
		if l.CPUQuota > 0 {
			opts = append(opts, fmt.Sprintf("cpu-quota=%d", l.CPUQuota), "cpu-period=100000")
		}
		if len(opts) > 0 {
			args = append(args, "--driver-opt", strings.Join(opts, ","))
		}
		if _, err := b.run(ctx, args...); err != nil {
			return fmt.Errorf("create builder %s: %w", b.name(), err)
		}
	}
	if _, err := b.run(ctx, "buildx", "inspect", "--bootstrap", b.name()); err != nil {
		return fmt.Errorf("start builder %s: %w", b.name(), err)
	}
	return nil
}

// Remove deletes the builder and its cache (tests and uninstall).
func (b *Builder) Remove(ctx context.Context) error {
	_, err := b.run(ctx, "buildx", "rm", "--force", b.name())
	return err
}

// Build builds and loads the image, then reads its Engine image ID. Builds
// are serialized: a second call waits for the first (ADR-0004: one build at a
// time by default).
func (b *Builder) Build(ctx context.Context, req Request) (Result, error) {
	if err := validate(req); err != nil {
		return Result{}, err
	}
	b.once.Do(func() { b.slot = make(chan struct{}, 1) })
	select {
	case b.slot <- struct{}{}:
		defer func() { <-b.slot }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tmp, err := os.MkdirTemp("", "shipyard-build-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	metaPath := filepath.Join(tmp, "metadata.json")
	tag := "shipyard/" + req.App + ":" + req.Commit[:12]

	// No build args or build environment carry anything from Shipyard
	// [DK-BUILD-SECRETS]. Attestations are off so --load behaves the same on
	// the classic and containerd image stores [DK-ATTEST].
	args := []string{"buildx", "build",
		"--builder", b.name(),
		"--load",
		"--provenance=false", "--sbom=false",
		"--progress=plain",
		"--metadata-file", metaPath,
		"--file", req.Dockerfile,
		"--tag", tag,
		"--label", "io.shipyard.managed=true",
		"--label", "io.shipyard.app=" + req.App,
		"--label", "io.shipyard.commit=" + req.Commit,
		"--label", "io.shipyard.deployment=" + req.DeploymentID,
		"--", req.Context,
	}
	logw := newBoundedLog(b.maxLog(), req.Log)
	cmd := exec.CommandContext(ctx, b.docker(), args...)
	cmd.Env = cleanEnv()
	cmd.Stdout, cmd.Stderr = logw, logw
	// Interrupt first so the buildx client cancels the build in BuildKit.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	runErr := cmd.Run()
	logw.Close()
	if runErr != nil {
		reason := runErr.Error()
		if ctx.Err() == context.DeadlineExceeded {
			reason = fmt.Sprintf("deadline of %s exceeded", timeout)
		}
		return Result{}, fmt.Errorf("%w: %s\n%s", ErrBuildFailed, reason, logw.Tail())
	}

	res := Result{Tag: tag}
	if meta, err := os.ReadFile(metaPath); err == nil {
		res.Metadata = meta
		var m map[string]any
		if json.Unmarshal(meta, &m) == nil {
			res.Digest, _ = m["containerimage.digest"].(string)
			res.ConfigDigest, _ = m["containerimage.config.digest"].(string)
		}
	}
	id, err := b.run(ctx, "image", "inspect", "--format", "{{.Id}}", "--", tag)
	if err != nil {
		return Result{}, fmt.Errorf("inspect built image: %w", err)
	}
	if !imageIDRE.MatchString(id) {
		return Result{}, fmt.Errorf("unexpected image ID %q", id)
	}
	res.ImageID = id
	return res, nil
}

func (b *Builder) maxLog() int {
	if b.MaxLog > 0 {
		return b.MaxLog
	}
	return DefaultMaxLog
}

func validate(r Request) error {
	switch {
	case !filepath.IsAbs(r.Dockerfile) || !filepath.IsAbs(r.Context):
		return errors.New("build: Dockerfile and context must be absolute, resolved paths")
	case !slugRE.MatchString(r.App):
		return fmt.Errorf("build: invalid app slug %q", r.App)
	case !shaRE.MatchString(r.Commit):
		return fmt.Errorf("build: invalid commit %q", r.Commit)
	case !uuidRE.MatchString(r.DeploymentID):
		return fmt.Errorf("build: invalid deployment id %q", r.DeploymentID)
	}
	return nil
}

// cleanEnv passes the docker CLI only what it needs, so no SHIPYARD_*
// variable (database URL, KEK location) can reach a build.
func cleanEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CONTEXT", "XDG_RUNTIME_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// run executes a short docker command and returns trimmed stdout.
func (b *Builder) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.docker(), args...)
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 1000 {
			msg = "…" + msg[len(msg)-1000:]
		}
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args[:min(len(args), 2)], " "), err, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// boundedLog forwards output line by line until max bytes, then emits one
// truncation notice and drops the rest, while always keeping the last lines
// for the failure message.
type boundedLog struct {
	pw   *io.PipeWriter
	done chan struct{}
	tail []string
}

const tailLines = 20

func newBoundedLog(max int, sink func(string)) *boundedLog {
	pr, pw := io.Pipe()
	l := &boundedLog{pw: pw, done: make(chan struct{})}
	go func() {
		defer close(l.done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		written, truncated := 0, false
		for sc.Scan() {
			line := sc.Text()
			l.tail = append(l.tail, line)
			if len(l.tail) > tailLines {
				l.tail = l.tail[1:]
			}
			if truncated || sink == nil {
				continue
			}
			if written+len(line)+1 > max {
				sink(fmt.Sprintf("… build log truncated at %d bytes; the last lines appear in the error", max))
				truncated = true
				continue
			}
			written += len(line) + 1
			sink(line)
		}
		io.Copy(io.Discard, pr) // a line over 1 MiB stops the scanner; keep draining
	}()
	return l
}

func (l *boundedLog) Write(p []byte) (int, error) { return l.pw.Write(p) }

// Close ends the stream and waits for the reader to finish.
func (l *boundedLog) Close() {
	l.pw.Close()
	<-l.done
}

// Tail returns the last lines of output. Call after Close.
func (l *boundedLog) Tail() string { return strings.Join(l.tail, "\n") }
