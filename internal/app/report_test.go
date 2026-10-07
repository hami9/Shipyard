package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

type fakeGitHub struct {
	calls     []string // what was reported, in order
	createErr error
}

func (g *fakeGitHub) CreateDeployment(_ context.Context, inst int64, repo string, d GitHubDeployment) (int64, error) {
	g.calls = append(g.calls, fmt.Sprintf("create %d %s ref=%s env=%s %q id=%s", inst, repo, d.Ref, d.Environment, d.Description, d.ShipyardID))
	if g.createErr != nil {
		return 0, g.createErr
	}
	return 9001, nil
}

func (g *fakeGitHub) CreateStatus(_ context.Context, inst int64, repo string, id int64, s GitHubStatus) error {
	g.calls = append(g.calls, fmt.Sprintf("status %d %s %q url=%s", id, s.State, s.Description, s.EnvironmentURL))
	return nil
}

func withGitHub(h *harness) *fakeGitHub {
	g := &fakeGitHub{}
	inst := int64(5)
	h.st.app.GitHubInstallationID = &inst
	h.d.GitHub = g
	return g
}

// P4.6: a deploy of an app with an installation is a GitHub Deployment of
// its commit in the app's environment: in progress from the moment the
// commit is known, then successful with the app's address; the release it
// replaced goes inactive.
func TestReportDeploy(t *testing.T) {
	h := newHarness()
	h.withRouter("web.example.com", "www.example.com")
	h.st.prev = &store.Deployment{ID: "dep-0", ContainerID: oldCtrID, GitHubDeploymentID: 70}
	g := withGitHub(h)
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`create 5 hami9/demo ref=` + sha + ` env=web "Shipyard deploy dep-1" id=dep-1`,
		`status 9001 in_progress "Deploying abababababab (Shipyard operation ` + opID + `)" url=`,
		`status 9001 success "Active: abababababab" url=https://web.example.com`,
		`status 70 inactive "Replaced by Shipyard deployment dep-1" url=`,
	}
	if !slices.Equal(g.calls, want) {
		t.Fatalf("reported:\n%s\nwant:\n%s", strings.Join(g.calls, "\n"), strings.Join(want, "\n"))
	}
	if h.st.dep.GitHubDeploymentID != 9001 || !h.logged("reporting to GitHub as deployment 9001 (environment web)") {
		t.Fatalf("recorded %d, events %v", h.st.dep.GitHubDeploymentID, h.st.events)
	}
}

// A failure is reported with its phase and the operation, never the error
// text: GitHub shows it to everyone who can read the repository.
func TestReportFailure(t *testing.T) {
	h := newHarness()
	h.bld.err = errors.New("secret-ish build output")
	g := withGitHub(h)
	if err := h.run(t, `{}`); err != nil {
		t.Fatal(err)
	}
	last := g.calls[len(g.calls)-1]
	if last != `status 9001 failure "Failed during build; see Shipyard operation `+opID+`" url=` || strings.Contains(strings.Join(g.calls, ""), "secret-ish") {
		t.Fatalf("reported %v", g.calls)
	}
	if h.st.opStatus != store.OpFailed {
		t.Fatalf("op = %s", h.st.opStatus)
	}
}

// Reporting never decides a deploy: GitHub refusing costs a warning event.
// Without an installation, or without a reporter, nothing is reported.
func TestReportBestEffort(t *testing.T) {
	h := newHarness()
	g := withGitHub(h)
	g.createErr = errors.New("403 Resource not accessible by integration")
	if err := h.run(t, `{}`); err != nil || h.st.opStatus != store.OpSucceeded {
		t.Fatalf("run: %v, op %s", err, h.st.opStatus)
	}
	if len(g.calls) != 1 || !h.logged("could not report to GitHub: 403") || h.st.dep.GitHubDeploymentID != 0 {
		t.Fatalf("calls %v, events %v", g.calls, h.st.events)
	}

	h = newHarness()
	g = &fakeGitHub{}
	h.d.GitHub = g // an app without an installation
	if err := h.run(t, `{}`); err != nil || len(g.calls) != 0 {
		t.Fatalf("no installation: %v, calls %v", err, g.calls)
	}
}

// A resumed deploy reports to the GitHub Deployment it recorded; a rollback
// says what it rolls back to.
func TestReportResumeAndRollback(t *testing.T) {
	h := newHarness()
	g := withGitHub(h)
	h.st.dep = &store.Deployment{ID: "dep-1", OperationID: opID, SourceCommitSHA: sha, ImageID: image, Status: store.DeployStarting,
		GitHubDeploymentID: 77}
	if err := h.d.Run(t.Context(), store.Operation{ID: opID, AppID: "app-1", Kind: KindDeploy, Attempt: 2}, owner); err != nil {
		t.Fatal(err)
	}
	if len(g.calls) != 2 || !strings.HasPrefix(g.calls[0], "status 77 in_progress") || !strings.HasPrefix(g.calls[1], "status 77 success") {
		t.Fatalf("resumed: %v", g.calls)
	}

	rh := rollbackHarness()
	g = withGitHub(rh)
	if err := rh.rollback(t, `{"target":"dep-old"}`); err != nil {
		t.Fatal(err)
	}
	if len(g.calls) == 0 || !strings.Contains(g.calls[0], `"Shipyard rollback to deployment dep-old"`) {
		t.Fatalf("rollback: %v", g.calls)
	}
}
