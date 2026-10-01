//go:build e2e

package e2e

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// crashState is what the database must say while the worker lies dead at a
// fault point: the operation's phase, and its deployment's status ("" when
// no deployment exists yet). candidate tells who answers through Caddy: the
// release that was active before, or already the new container.
type crashState struct {
	phase, deployment string
	candidate         bool
}

var crashStates = map[string]crashState{
	app.PhaseFetch:     {app.PhaseFetch, "", false},
	app.PhaseBuild:     {app.PhaseBuild, store.DeployBuilding, false},
	app.FaultBuilt:     {app.PhaseBuild, store.DeployBuilding, false},
	app.PhaseStart:     {app.PhaseStart, store.DeployStarting, false},
	app.FaultCreated:   {app.PhaseStart, store.DeployStarting, false},
	app.FaultStarted:   {app.PhaseStart, store.DeployStarting, false},
	app.PhaseHealth:    {app.PhaseHealth, store.DeployHealthChecking, false},
	app.PhaseSwitch:    {app.PhaseSwitch, store.DeployHealthChecking, false},
	app.FaultSwitched:  {app.PhaseSwitch, store.DeploySwitching, true},
	app.PhaseActivate:  {app.PhaseActivate, store.DeploySwitching, true},
	app.FaultCommitted: {app.PhaseActivate, store.DeployActive, true},
}

// The crash-safety suite (P3.7, invariant 3). For every fault point of a
// deploy, a worker is killed there as by `kill -9`, the state it left is
// checked, and a fresh worker must finish the same operation:
//
//   - while the worker is dead, the app keeps answering, exactly one
//     deployment is active, and the database shows the expected phase;
//   - after the restart, the operation succeeds on its second attempt with
//     the one deployment it started, the previous release is superseded and
//     drained, and exactly one container is left: nothing duplicated,
//     nothing orphaned;
//   - Caddy serves the new release.
//
// No subtests: the harness reports through the test's own goroutine.
func TestCrashSafety(t *testing.T) {
	h := start(t)
	slug := h.slug
	const host = "crash.e2e.example"
	h.cli("", "app", "create", slug, "--repo", "e2e/demo", "--branch", "main", "--port", "8080", "--health-path", "/healthz")
	h.cli("", "domain", "add", slug, host)
	first := h.deploy("--ref", h.repo.good)
	h.wantOp(first, "succeeded", "")
	h.stopWorker() // from here on, the suite runs its own workers

	pool, err := store.Open(t.Context(), h.dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := store.New(pool)
	ctx := t.Context()
	firstOp, err := s.OperationByID(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	appID := firstOp.AppID
	// A short lease, so a dead worker's operation is taken over quickly, and
	// a short observation window, so the drain is part of each round.
	env := append(slices.Clone(h.workerEnv), "SHIPYARD_WORKER_LEASE=3s", "SHIPYARD_OBSERVATION_WINDOW=1s")
	served := func() string { return h.viaCaddy(host, "/read?path=/etc/hostname") }

	for _, point := range app.FaultPoints {
		want, ok := crashStates[point]
		if !ok {
			t.Fatalf("fault point %q has no expected state in this suite", point)
		}
		before, err := s.ActiveDeployment(ctx, appID)
		if err != nil {
			t.Fatalf("%s: %v", point, err)
		}
		history, _ := s.Releases(ctx, appID, "", 100)

		doomed := h.spawn("worker (dies at "+point+")", append(slices.Clone(env), "SHIPYARD_TEST_CRASH_AT="+point), "shipyard-worker", "run")
		op := h.deploy("--ref", h.repo.good)
		select {
		case <-doomed.exited:
		case <-time.After(3 * time.Minute):
			t.Fatalf("%s: the worker did not die there", point)
		}

		// The worker is dead. What it left behind:
		o, err := s.OperationByID(ctx, op)
		if err != nil {
			t.Fatalf("%s: %v", point, err)
		}
		wantStatus := store.OpRunning
		if point == app.FaultCommitted {
			wantStatus = store.OpSucceeded // the commit also completes the operation
		}
		if o.Status != wantStatus || o.Phase != want.phase || o.Attempt != 1 {
			t.Fatalf("%s: operation is %s in phase %q, attempt %d; want %s in %q, attempt 1", point, o.Status, o.Phase, o.Attempt, wantStatus, want.phase)
		}
		dep, err := s.DeploymentByOperation(ctx, op)
		switch {
		case want.deployment == "" && !errors.Is(err, store.ErrNotFound):
			t.Fatalf("%s: a deployment exists already: %+v, %v", point, dep, err)
		case want.deployment != "" && (err != nil || dep.Status != want.deployment):
			t.Fatalf("%s: deployment is %q (%v), want %s", point, dep.Status, err, want.deployment)
		}
		active, err := s.ActiveDeployment(ctx, appID)
		if wantActive := map[bool]string{false: before.ID, true: dep.ID}[point == app.FaultCommitted]; err != nil || active.ID != wantActive {
			t.Fatalf("%s: active deployment is %s (%v), want %s", point, active.ID, err, wantActive)
		}
		// The app answers all along: from the release that was active, or,
		// once Caddy was switched, from the candidate that passed its gate.
		serving := before.ContainerID
		if want.candidate {
			serving = dep.ContainerID
		}
		if got := served(); serving == "" || got != serving[:12] {
			t.Fatalf("%s: with the worker dead, caddy serves %q, want %.12s", point, got, serving)
		}

		// A fresh worker takes over.
		next := h.spawn("worker (after "+point+")", env, "shipyard-worker", "run")
		h.wantOp(op, "succeeded", "")
		o, _ = s.OperationByID(ctx, op)
		if wantAttempt := map[bool]int{false: 2, true: 1}[point == app.FaultCommitted]; o.Attempt != wantAttempt {
			t.Fatalf("%s: finished on attempt %d, want %d", point, o.Attempt, wantAttempt)
		}
		done, err := s.DeploymentByOperation(ctx, op)
		if err != nil || done.Status != store.DeployActive || (dep.ID != "" && done.ID != dep.ID) {
			t.Fatalf("%s: the operation's deployment is %s %s (%v); it began as %s", point, done.ID, done.Status, err, dep.ID)
		}
		if old, _ := s.DeploymentByID(ctx, before.ID); old.Status != store.DeploySuperseded {
			t.Fatalf("%s: the previous deployment is %s, want superseded", point, old.Status)
		}
		if now, _ := s.Releases(ctx, appID, "", 100); len(now) != len(history)+1 {
			t.Fatalf("%s: the history grew by %d deployments, want 1", point, len(now)-len(history))
		}
		// Drained: one container is left, the active deployment's.
		var left []string
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			left = strings.Fields(h.docker("ps", "-aq", "--no-trunc", "--filter", "label=io.shipyard.app="+slug))
			if len(left) == 1 && left[0] == done.ContainerID {
				break
			}
		}
		if len(left) != 1 || left[0] != done.ContainerID {
			t.Fatalf("%s: containers left: %v, want only %s", point, left, done.ContainerID)
		}
		if got := served(); got != done.ContainerID[:12] {
			t.Fatalf("%s: after recovery caddy serves %q, want %.12s", point, got, done.ContainerID)
		}
		next.stop()
		t.Logf("%s: recovered (operation %s, deployment %s)", point, op, done.ID)
	}
}
