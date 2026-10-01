package app

import (
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

const staleRev = "44444444-4444-4444-8444-444444444444"

func rebuildHarness() *harness {
	h := newHarness()
	rev := revID
	commit := strings.Repeat("cd", 20)
	h.st.byID = map[string]store.Deployment{
		"dep-active": {ID: "dep-active", AppID: "app-1", SourceCommitSHA: commit, ImageID: oldImage, EnvRevisionID: &rev, Status: store.DeployActive},
		"dep-bare":   {ID: "dep-bare", AppID: "app-1", SourceCommitSHA: commit, ImageID: oldImage, Status: store.DeployActive},
		"dep-old":    {ID: "dep-old", AppID: "app-1", SourceCommitSHA: commit, ImageID: oldImage, Status: store.DeploySuperseded},
		"dep-other":  {ID: "dep-other", AppID: "app-2", SourceCommitSHA: commit, ImageID: oldImage, Status: store.DeployActive},
	}
	// The app's latest revision is one fakeEnv cannot resolve: a rebuild that
	// succeeds used the revision of the deployment it replaces.
	h.st.rev = &store.EnvRevision{ID: staleRev}
	return h
}

// P3.6a: a rebuild is an ordinary deploy of the active deployment's commit,
// pinned to the environment revision that deployment ran with, not the
// latest one.
func TestRebuild(t *testing.T) {
	h := rebuildHarness()
	if err := h.run(t, `{"rebuild_of":"dep-active"}`); err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("cd", 20)
	if h.src.fetched != 1 || h.src.req.Ref != commit || h.bld.built != 1 || h.bld.req.Commit != commit {
		t.Fatalf("fetched %d (ref %q), built %d (commit %q)", h.src.fetched, h.src.req.Ref, h.bld.built, h.bld.req.Commit)
	}
	d := h.st.dep
	if d.SourceCommitSHA != commit || d.EnvRevisionID == nil || *d.EnvRevisionID != revID || d.Status != store.DeployActive ||
		h.st.opStatus != store.OpSucceeded || h.rt.created[0].Env["API_KEY"] != "s3cret" {
		t.Fatalf("deployment = %+v, op %s %q", d, h.st.opStatus, h.st.opReason)
	}
	if !h.logged("rebuilding deployment dep-active") || !h.logged("is gone from this host") {
		t.Errorf("events = %v", h.st.events)
	}

	// A deployment that ran without an environment is rebuilt without one.
	h = rebuildHarness()
	if err := h.run(t, `{"rebuild_of":"dep-bare"}`); err != nil {
		t.Fatal(err)
	}
	if h.st.opStatus != store.OpSucceeded || h.st.dep.EnvRevisionID != nil || len(h.rt.created[0].Env) != 0 {
		t.Fatalf("op %s %q, revision %v, env %v", h.st.opStatus, h.st.opReason, h.st.dep.EnvRevisionID, h.rt.created[0].Env)
	}
}

// A deployment that is no longer active is not rebuilt: building its commit
// would move the app backwards. Nor is another app's, or an unknown one.
// The operation fails before any fetch, build, or container.
func TestRebuildRefused(t *testing.T) {
	for id, want := range map[string]string{
		"dep-old":   "deployment dep-old is superseded, no longer active: nothing to rebuild",
		"dep-other": "is not a deployment of this app",
		"dep-nope":  "does not exist",
	} {
		t.Run(id, func(t *testing.T) {
			h := rebuildHarness()
			if err := h.run(t, `{"rebuild_of":"`+id+`"}`); err != nil {
				t.Fatal(err)
			}
			if h.st.opStatus != store.OpFailed || !strings.Contains(h.st.opReason, want) {
				t.Fatalf("op %s %q, want failed with %q", h.st.opStatus, h.st.opReason, want)
			}
			if h.st.dep != nil || h.src.fetched != 0 || h.bld.built != 0 || len(h.rt.created) != 0 {
				t.Fatalf("side effects: dep %v, fetched %d, built %d, created %v", h.st.dep, h.src.fetched, h.bld.built, h.rt.created)
			}
		})
	}
}
