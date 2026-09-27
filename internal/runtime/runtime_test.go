package runtime

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

func validSpec() Spec {
	return Spec{
		App:          "web",
		DeploymentID: "11111111-1111-4111-8111-111111111111",
		Commit:       strings.Repeat("ab", 20),
		Image:        "sha256:" + strings.Repeat("0f", 32),
		Env:          map[string]string{"B": "2", "A": "1"},
		CPUs:         0.5,
		Memory:       64 << 20,
		StopTimeout:  1500 * time.Millisecond,
	}
}

func TestSpecValidate(t *testing.T) {
	if err := validSpec().Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Spec)
	}{
		{"empty slug", func(s *Spec) { s.App = "" }},
		{"slug with dot", func(s *Spec) { s.App = "a.b" }},
		{"deployment not uuid", func(s *Spec) { s.DeploymentID = "x" }},
		{"short commit", func(s *Spec) { s.Commit = "abc" }},
		{"image tag", func(s *Spec) { s.Image = "nginx:latest" }},
		{"image digest prefix", func(s *Spec) { s.Image = "sha256:abc" }},
		{"zero cpus", func(s *Spec) { s.CPUs = 0 }},
		{"negative cpus", func(s *Spec) { s.CPUs = -1 }},
		{"NaN cpus", func(s *Spec) { s.CPUs = math.NaN() }},
		{"infinite cpus", func(s *Spec) { s.CPUs = math.Inf(1) }},
		{"memory below Docker minimum", func(s *Spec) { s.Memory = 6<<20 - 1 }},
		{"negative pids", func(s *Spec) { s.PidsLimit = -1 }},
		{"negative stop timeout", func(s *Spec) { s.StopTimeout = -time.Second }},
		{"stop timeout over 10m", func(s *Spec) { s.StopTimeout = 11 * time.Minute }},
		{"env key with =", func(s *Spec) { s.Env = map[string]string{"A=B": "x"} }},
		{"env key starting with digit", func(s *Spec) { s.Env = map[string]string{"1A": "x"} }},
		{"env value with NUL", func(s *Spec) { s.Env = map[string]string{"A": "x\x00y"} }},
		{"dangerous capability", func(s *Spec) { s.CapAdd = []string{"SYS_ADMIN"} }},
		{"capability ALL", func(s *Spec) { s.CapAdd = []string{"ALL"} }},
		{"lowercase capability", func(s *Spec) { s.CapAdd = []string{"chown"} }},
		{"NET_RAW", func(s *Spec) { s.CapAdd = []string{"NET_RAW"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSpec()
			tt.mutate(&s)
			if err := s.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

// Invariant 8: a rejected value never appears in the error.
func TestValidateHidesEnvValues(t *testing.T) {
	s := validSpec()
	s.Env = map[string]string{"TOKEN": "hunter2\x00secret"}
	err := s.Validate()
	if err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("err = %v", err)
	}
}

// Invariant 10, checked without Docker: the request itself is hardened. The
// docker-tagged tests confirm the Engine applies it.
func TestCreateOptionsHardened(t *testing.T) {
	s := validSpec()
	s.CapAdd = []string{"NET_BIND_SERVICE"}
	o := s.createOptions()
	h, c := o.HostConfig, o.Config

	if o.Name != "shipyard-web-11111111-1111-4111-8111-111111111111" {
		t.Errorf("name = %q", o.Name)
	}
	if c.Image != s.Image {
		t.Errorf("image = %q", c.Image)
	}
	if !slices.Equal(c.Env, []string{"A=1", "B=2"}) {
		t.Errorf("env = %q, want sorted KEY=value", c.Env)
	}
	if c.StopTimeout == nil || *c.StopTimeout != 2 {
		t.Errorf("stop timeout = %v, want 1.5s rounded up to 2", c.StopTimeout)
	}
	for k, v := range map[string]string{labelManaged: "true", labelApp: "web", labelDeployment: s.DeploymentID, labelCommit: s.Commit} {
		if c.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, c.Labels[k], v)
		}
	}
	if c.ExposedPorts != nil {
		t.Errorf("exposed ports = %v", c.ExposedPorts)
	}

	if !slices.Equal(h.CapDrop, []string{"ALL"}) || !slices.Equal(h.CapAdd, []string{"NET_BIND_SERVICE"}) {
		t.Errorf("caps drop=%v add=%v", h.CapDrop, h.CapAdd)
	}
	if !slices.Equal(h.SecurityOpt, []string{"no-new-privileges"}) {
		t.Errorf("security opts = %v", h.SecurityOpt)
	}
	if h.Privileged || h.PublishAllPorts || len(h.PortBindings) != 0 || len(h.Binds) != 0 || len(h.Mounts) != 0 {
		t.Errorf("forbidden options set: %+v", h)
	}
	if h.NetworkMode != "shipyard-app-web" {
		t.Errorf("network mode = %q", h.NetworkMode)
	}
	if h.PidMode != "" || h.IpcMode != "" || h.UTSMode != "" || h.UsernsMode != "" {
		t.Errorf("host namespaces shared: pid=%q ipc=%q uts=%q userns=%q", h.PidMode, h.IpcMode, h.UTSMode, h.UsernsMode)
	}
	if h.LogConfig.Type != "local" {
		t.Errorf("log driver = %q", h.LogConfig.Type)
	}
	if h.RestartPolicy.Name != container.RestartPolicyUnlessStopped {
		t.Errorf("restart = %q", h.RestartPolicy.Name)
	}
	if h.Memory != 64<<20 || h.NanoCPUs != 500_000_000 || h.PidsLimit == nil || *h.PidsLimit != DefaultPidsLimit {
		t.Errorf("limits memory=%d nanocpus=%d pids=%v", h.Memory, h.NanoCPUs, h.PidsLimit)
	}
	if h.ReadonlyRootfs || h.Init == nil || *h.Init {
		t.Errorf("opt-ins should be off: readonly=%v init=%v", h.ReadonlyRootfs, h.Init)
	}
	eps := o.NetworkingConfig.EndpointsConfig
	if len(eps) != 1 || eps["shipyard-app-web"] == nil {
		t.Errorf("endpoints = %v", eps)
	}

	s.PidsLimit, s.ReadOnly, s.Init = 64, true, true
	h = s.createOptions().HostConfig
	if *h.PidsLimit != 64 || !h.ReadonlyRootfs || !*h.Init {
		t.Errorf("explicit pids and opt-ins not applied: %+v", h)
	}
}

// The spec must not share its slices with the request.
func TestCreateOptionsCopiesCaps(t *testing.T) {
	s := validSpec()
	s.CapAdd = []string{"CHOWN"}
	o := s.createOptions()
	s.CapAdd[0] = "SYS_ADMIN"
	if o.HostConfig.CapAdd[0] != "CHOWN" {
		t.Fatal("CapAdd aliases the spec")
	}
}
