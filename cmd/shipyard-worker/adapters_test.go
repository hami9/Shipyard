package main

import (
	"testing"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/runtime"
)

// The API computes a running deployment's upstream from the naming
// convention (app.ContainerName) without asking Docker; the runtime names
// containers. They must agree, or a new domain would point at nothing.
func TestContainerNamesAgree(t *testing.T) {
	const dep = "11111111-1111-4111-8111-111111111111"
	for _, slug := range []string{"web", "a", "my-app-2"} {
		if a, r := app.ContainerName(slug, dep), runtime.ContainerName(slug, dep); a != r {
			t.Errorf("%s: app %q, runtime %q", slug, a, r)
		}
	}
}
