package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Invariant 1 (CLAUDE.md §3): shipyard-api links nothing that talks to
// Docker, BuildKit, Caddy's admin API, or git, and cannot start programs.
// The import graph is the proof, so a later import cannot slip in (P5.8).
func TestAPIDependencies(t *testing.T) {
	gobin, err := exec.LookPath("go") // go test puts GOROOT/bin first on PATH
	if err != nil {
		t.Skip("the go command is not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", "-tags", "integration,docker,e2e", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	if len(deps) < 50 {
		t.Fatalf("go list gave %d packages; expected the whole graph", len(deps))
	}
	const self = "github.com/hami9/shipyard/internal/"
	forbidden := []string{
		"github.com/moby/", "github.com/docker/", // the Docker client
		"os/exec", // no programs: not git, not docker, not buildx
		self + "runtime", self + "build", self + "source", self + "routing",
		self + "reconcile", self + "github", self + "backup", self + "health", self + "monitor",
	}
	for _, d := range deps {
		for _, f := range forbidden {
			if d == f || (strings.HasSuffix(f, "/") && strings.HasPrefix(d, f)) {
				t.Errorf("shipyard-api depends on %s (invariant 1)", d)
			}
		}
	}
}
