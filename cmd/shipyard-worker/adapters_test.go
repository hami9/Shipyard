package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/github"
	"github.com/hami9/shipyard/internal/runtime"
	"github.com/hami9/shipyard/internal/source"
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

type fakeMinter struct {
	calls []string
	err   error
}

func (f *fakeMinter) InstallationToken(_ context.Context, id int64, repo string) (github.Token, error) {
	f.calls = append(f.calls, repo)
	return github.Token{}, f.err
}

// P4.4: an app with an installation needs the worker's GitHub App, and a
// refused token ends the fetch before git runs.
func TestSourceAdapterInstallation(t *testing.T) {
	req := app.FetchRequest{OperationID: "00000000-0000-4000-8000-000000000001", Repo: "octo/private", Branch: "main", InstallationID: 7}
	f := &source.Fetcher{Root: t.TempDir(), BaseURL: "http://127.0.0.1:1", Git: "/nonexistent/git"}

	if _, err := (sourceAdapter{f: f}).Fetch(t.Context(), req); err == nil || !strings.Contains(err.Error(), "SHIPYARD_GITHUB_APP_ID") {
		t.Fatalf("no GitHub App: %v", err)
	}
	m := &fakeMinter{err: errors.New("GitHub refused an installation token")}
	if _, err := (sourceAdapter{f: f, gh: m}).Fetch(t.Context(), req); err == nil || !strings.Contains(err.Error(), "refused") ||
		len(m.calls) != 1 || m.calls[0] != "octo/private" {
		t.Fatalf("refused token: %v, calls %v", err, m.calls)
	}
	// A public app never asks for a token.
	req.InstallationID = 0
	(sourceAdapter{f: f, gh: m}).Fetch(t.Context(), req)
	if len(m.calls) != 1 {
		t.Fatalf("a public fetch asked for a token: %v", m.calls)
	}
}
