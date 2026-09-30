package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

const oldImage = "sha256:1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e"

func (h *harness) rollback(t *testing.T, payload string) error {
	t.Helper()
	return h.d.Run(t.Context(), store.Operation{ID: opID, AppID: "app-1", Kind: KindRollback, Payload: json.RawMessage(payload)}, owner)
}

func rollbackHarness() *harness {
	h := newHarness()
	oldRev := revID
	h.st.byID = map[string]store.Deployment{
		"dep-old":   {ID: "dep-old", AppID: "app-1", SourceCommitSHA: strings.Repeat("cd", 20), ImageID: oldImage, EnvRevisionID: &oldRev, Status: store.DeploySuperseded},
		"dep-other": {ID: "dep-other", AppID: "app-2", ImageID: oldImage, Status: store.DeploySuperseded},
	}
	h.st.prev = &store.Deployment{ID: "dep-0", ContainerID: oldCtrID}
	return h
}

// P3.3: a rollback runs the target's image again, with the environment
// revision it ran with, through the same health gate, switch, and
// activation; nothing is fetched or built (invariant 6).
func TestRollback(t *testing.T) {
	h := rollbackHarness()
	h.withRouter("web.example.com")
	if err := h.rollback(t, `{"target":"dep-old"}`); err != nil {
		t.Fatal(err)
	}
	if h.src.fetched != 0 || h.bld.built != 0 {
		t.Fatalf("fetched %d, built %d", h.src.fetched, h.bld.built)
	}
	if want := []string{PhaseStart, PhaseHealth, PhaseSwitch, PhaseActivate}; !slices.Equal(h.st.phases, want) {
		t.Errorf("phases = %v", h.st.phases)
	}
	c := h.rt.created[0]
	if c.Image != oldImage || c.Commit != strings.Repeat("cd", 20) || c.Env["API_KEY"] != "s3cret" || c.DeploymentID != "dep-1" {
		t.Errorf("container = %+v", c)
	}
	d := h.st.dep
	if d.Kind != KindRollback || d.SourceDeployment == nil || *d.SourceDeployment != "dep-old" || d.Status != store.DeployActive ||
		d.EnvRevisionID == nil || *d.EnvRevisionID != revID || h.st.opStatus != store.OpSucceeded {
		t.Errorf("deployment = %+v, op %s", d, h.st.opStatus)
	}
	if !h.logged("rolling back to deployment dep-old") || !h.logged("the configuration it ran with") {
		t.Errorf("events = %v", h.st.events)
	}
}

// With the current configuration, the latest revision replaces the one the
// target ran with; with no environment at all, none.
func TestRollbackWithCurrentConfig(t *testing.T) {
	h := rollbackHarness()
	// The target ran with a revision fakeEnv cannot resolve; only the latest
	// (revID) works, so a success proves the latest one was used.
	old := h.st.byID["dep-old"]
	stale := "44444444-4444-4444-8444-444444444444"
	old.EnvRevisionID = &stale
	h.st.byID["dep-old"] = old
	h.st.rev = &store.EnvRevision{ID: revID}
	if err := h.rollback(t, `{"target":"dep-old","with_current_config":true}`); err != nil {
		t.Fatal(err)
	}
	if h.st.opStatus != store.OpSucceeded || *h.st.dep.EnvRevisionID != revID || h.rt.created[0].Env["API_KEY"] != "s3cret" ||
		!h.logged("the current configuration") {
		t.Fatalf("op %s %q, revision %v, events %v", h.st.opStatus, h.st.opReason, h.st.dep.EnvRevisionID, h.st.events)
	}

	h = rollbackHarness()
	if err := h.rollback(t, `{"target":"dep-old","with_current_config":true}`); err != nil {
		t.Fatal(err)
	}
	if h.st.dep.EnvRevisionID != nil || len(h.rt.created[0].Env) != 0 {
		t.Fatalf("no environment: revision %v, env %v", h.st.dep.EnvRevisionID, h.rt.created[0].Env)
	}
}

// An image that is gone makes the rollback unavailable, final and before any
// side effect; so does a target that is not this app's.
func TestRollbackRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		payload string
		images  map[string]bool
		want    string
	}{
		"image gone":   {`{"target":"dep-old"}`, map[string]bool{}, "rollback unavailable: image " + oldImage},
		"other app":    {`{"target":"dep-other"}`, nil, "not a built deployment of this app"},
		"unknown":      {`{"target":"dep-nope"}`, nil, "does not exist"},
		"no target":    {`{}`, nil, "invalid rollback payload"},
		"not a object": {`[]`, nil, "invalid rollback payload"},
	} {
		t.Run(name, func(t *testing.T) {
			h := rollbackHarness()
			h.rt.images = tc.images
			if err := h.rollback(t, tc.payload); err != nil {
				t.Fatal(err)
			}
			if h.st.opStatus != store.OpFailed || !strings.Contains(h.st.opReason, tc.want) {
				t.Fatalf("op %s %q, want failed with %q", h.st.opStatus, h.st.opReason, tc.want)
			}
			if h.st.dep != nil || len(h.rt.created) != 0 || len(h.rt.calls) != 0 {
				t.Fatalf("side effects: dep %v, created %v, calls %v", h.st.dep, h.rt.created, h.rt.calls)
			}
		})
	}
}

// A retried rollback resumes its deployment instead of creating another.
func TestRollbackResumes(t *testing.T) {
	h := rollbackHarness()
	src := "dep-old"
	h.st.dep = &store.Deployment{ID: "dep-1", Kind: KindRollback, SourceDeployment: &src, ImageID: oldImage, Status: store.DeployStarting}
	h.rt.images = map[string]bool{} // gone now, but the rollback was already prepared
	if err := h.d.Run(t.Context(), store.Operation{ID: opID, AppID: "app-1", Kind: KindRollback, Attempt: 2,
		Payload: json.RawMessage(`{"target":"dep-old"}`)}, owner); err != nil {
		t.Fatal(err)
	}
	if len(h.rt.created) != 1 || h.rt.created[0].Image != oldImage || h.st.dep.Status != store.DeployActive || !h.logged("resuming deployment dep-1") {
		t.Fatalf("resume: created %v, dep %+v, events %v", h.rt.created, h.st.dep, h.st.events)
	}
}

func TestRotatedSecrets(t *testing.T) {
	id := func(s string) *string { return &s }
	old := []store.EnvEntry{
		{Key: "API_KEY", SecretValueID: id("v1")},   // unchanged
		{Key: "DB_URL", SecretValueID: id("v2")},    // rotated
		{Key: "GONE", SecretValueID: id("v3")},      // removed since
		{Key: "LEVEL", PlainValue: id("info")},      // plain: never counts
		{Key: "NOW_PLAIN", SecretValueID: id("v4")}, // made plain since
	}
	latest := []store.EnvEntry{
		{Key: "API_KEY", SecretValueID: id("v1")},
		{Key: "DB_URL", SecretValueID: id("v9")},
		{Key: "LEVEL", PlainValue: id("debug")},
		{Key: "NEW", SecretValueID: id("v5")},
		{Key: "NOW_PLAIN", PlainValue: id("x")},
	}
	if got := RotatedSecrets(old, latest); !slices.Equal(got, []string{"DB_URL", "GONE", "NOW_PLAIN"}) {
		t.Fatalf("rotated = %v", got)
	}
	if got := RotatedSecrets(nil, latest); got != nil {
		t.Fatalf("no old revision = %v", got)
	}
}
