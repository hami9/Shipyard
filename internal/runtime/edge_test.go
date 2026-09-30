package runtime

import (
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

func validEdge() EdgeSpec {
	return EdgeSpec{Name: "shipyard-caddy", Image: DefaultEdgeImage, AdminDir: "/run/shipyard/caddy", GID: 990,
		HTTPPort: 80, HTTPSPort: 443}
}

func TestEdgeSpecValidate(t *testing.T) {
	if err := validEdge().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*EdgeSpec){
		"empty name":           func(s *EdgeSpec) { s.Name = "" },
		"app network name":     func(s *EdgeSpec) { s.Name = "shipyard-app-web" },
		"uppercase name":       func(s *EdgeSpec) { s.Name = "Caddy" },
		"no image":             func(s *EdgeSpec) { s.Image = "" },
		"relative admin dir":   func(s *EdgeSpec) { s.AdminDir = "run/caddy" },
		"unclean admin dir":    func(s *EdgeSpec) { s.AdminDir = "/run/../etc" },
		"admin dir with pipe":  func(s *EdgeSpec) { s.AdminDir = "/run/x|0777" },
		"admin dir with comma": func(s *EdgeSpec) { s.AdminDir = "/run/x,ro" },
		"root group":           func(s *EdgeSpec) { s.GID = 0 },
		"relative API dir":     func(s *EdgeSpec) { s.APISocketDir = "run/api" },
		"API dir is root":      func(s *EdgeSpec) { s.APISocketDir = "/" },
		"API dir is admin dir": func(s *EdgeSpec) { s.APISocketDir = "/run/shipyard/caddy" },
		"API dir with colon":   func(s *EdgeSpec) { s.APISocketDir = "/run/api:rw" },
		"port too high":        func(s *EdgeSpec) { s.HTTPSPort = 70000 },
		"negative port":        func(s *EdgeSpec) { s.HTTPPort = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			s := validEdge()
			mutate(&s)
			if err := s.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// Invariants 11 and 12 as the request states them; the docker test checks
// that the Engine and Caddy honor them.
func TestEdgeCreateOptions(t *testing.T) {
	s := validEdge()
	o := s.createOptions()
	c, h := o.Config, o.HostConfig

	if c.Image != DefaultEdgeImage || !slices.Equal(c.Cmd, []string{"caddy", "run", "--resume"}) || c.User != "0:990" {
		t.Errorf("config = image %s cmd %v user %s", c.Image, c.Cmd, c.User)
	}
	if !slices.Equal(c.Env, []string{"CADDY_ADMIN=unix//run/shipyard/caddy/caddy-admin.sock|0660"}) {
		t.Errorf("env = %q", c.Env)
	}
	if c.Labels[labelManaged] != "true" || c.Labels[labelRole] != roleEdge || c.Labels[labelSpec] != s.hash() || c.Labels[labelApp] != "" {
		t.Errorf("labels = %v", c.Labels)
	}
	want := map[string]string{"80/tcp": "80", "443/tcp": "443", "443/udp": "443"}
	if len(h.PortBindings) != len(want) {
		t.Errorf("port bindings = %v", h.PortBindings)
	}
	for p, port := range want {
		b := h.PortBindings[network.MustParsePort(p)]
		if len(b) != 1 || b[0].HostPort != port || b[0].HostIP.IsValid() {
			t.Errorf("%s -> %v, want all interfaces:%s", p, b, port)
		}
	}
	if !slices.Equal(h.CapDrop, []string{"ALL"}) || !slices.Equal(h.CapAdd, []string{"NET_BIND_SERVICE"}) ||
		!slices.Equal(h.SecurityOpt, []string{"no-new-privileges"}) || !h.ReadonlyRootfs || h.Privileged {
		t.Errorf("hardening: %+v", h)
	}
	if h.LogConfig.Type != "local" || h.RestartPolicy.Name != container.RestartPolicyUnlessStopped ||
		h.Memory != edgeMemory || h.NanoCPUs != 1e9 || *h.PidsLimit != edgePids || string(h.NetworkMode) != "shipyard-caddy" {
		t.Errorf("host config: %+v", h)
	}
	if len(h.Binds) != 0 {
		t.Errorf("binds = %v", h.Binds)
	}
	var binds []mount.Mount
	for _, m := range h.Mounts {
		if m.Type == mount.TypeBind {
			binds = append(binds, m)
		}
	}
	// The admin socket directory is the only host path Caddy sees.
	if len(binds) != 1 || binds[0].Source != "/run/shipyard/caddy" || binds[0].Target != "/run/shipyard/caddy" || len(h.Mounts) != 3 {
		t.Errorf("mounts = %+v", h.Mounts)
	}

	s.BindIP, s.HTTPPort = netip.MustParseAddr("127.0.0.1"), 0
	b := s.createOptions().HostConfig.PortBindings[network.MustParsePort("80/tcp")]
	if b[0].HostIP.String() != "127.0.0.1" || b[0].HostPort != "" {
		t.Errorf("loopback, random port: %v", b)
	}

	// P2.8: the API's socket directory, read-only, at the same path.
	s.APISocketDir = "/run/shipyard-api"
	ms := s.createOptions().HostConfig.Mounts
	if last := ms[len(ms)-1]; len(ms) != 4 || last.Type != mount.TypeBind || last.Source != "/run/shipyard-api" ||
		last.Target != "/run/shipyard-api" || !last.ReadOnly {
		t.Errorf("mounts with the API = %+v", ms)
	}
}

// Any change of spec changes the label that triggers a recreate.
func TestEdgeSpecHash(t *testing.T) {
	a := validEdge()
	for name, mutate := range map[string]func(*EdgeSpec){
		"image": func(s *EdgeSpec) { s.Image = "caddy:2" },
		"port":  func(s *EdgeSpec) { s.HTTPSPort = 8443 },
		"dir":   func(s *EdgeSpec) { s.AdminDir = "/run/other" },
		"group": func(s *EdgeSpec) { s.GID = 991 },
		"bind":  func(s *EdgeSpec) { s.BindIP = netip.MustParseAddr("10.0.0.1") },
		"api":   func(s *EdgeSpec) { s.APISocketDir = "/run/shipyard-api" },
	} {
		b := validEdge()
		mutate(&b)
		if a.hash() == b.hash() {
			t.Errorf("%s: hash unchanged", name)
		}
	}
	if a.hash() != validEdge().hash() {
		t.Error("hash is not deterministic")
	}
	// An edge without the API keeps the hash it had before P2.8, so an
	// upgrade does not recreate it: the field is omitted when empty.
	if b, _ := json.Marshal(a); strings.Contains(string(b), "APISocketDir") {
		t.Errorf("spec JSON = %s", b)
	}
}
