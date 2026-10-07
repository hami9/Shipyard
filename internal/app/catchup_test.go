package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

type catchUpFakes struct {
	apps     []store.CatchUpApp
	heads    map[string]string // repo -> head; missing: an error
	tried    map[string]bool   // app:sha the store refuses
	listErr  error
	enqueued []string // app:branch:sha
	asked    []string // repo#installation
}

func (f *catchUpFakes) CatchUpCandidates(context.Context) ([]store.CatchUpApp, error) {
	return f.apps, f.listErr
}

func (f *catchUpFakes) EnqueueCatchUp(_ context.Context, appID, branch, sha string) (store.Operation, bool, error) {
	if appID == "broken" {
		return store.Operation{}, false, errors.New("db down")
	}
	f.enqueued = append(f.enqueued, appID+":"+branch+":"+sha)
	if f.tried[appID+":"+sha] {
		return store.Operation{}, false, nil
	}
	return store.Operation{ID: "op-" + appID}, true, nil
}

func (f *catchUpFakes) Head(_ context.Context, repo, branch string, installationID int64) (string, error) {
	f.asked = append(f.asked, repo+"#"+string(rune('0'+installationID)))
	if h, ok := f.heads[repo]; ok {
		return h, nil
	}
	return "", errors.New("branch not found")
}

// P4.5: each candidate's head goes to the store, which decides; a head
// that cannot be read, or a store error, is reported and the rest go on.
func TestCatchUp(t *testing.T) {
	f := &catchUpFakes{
		apps: []store.CatchUpApp{
			{ID: "a", Slug: "web", Repo: "o/web", Branch: "main"},
			{ID: "b", Slug: "api", Repo: "o/api", Branch: "dev", InstallationID: 7},
			{ID: "c", Slug: "gone", Repo: "o/gone", Branch: "main"},
			{ID: "broken", Slug: "bad", Repo: "o/bad", Branch: "main"},
			{ID: "d", Slug: "old", Repo: "o/old", Branch: "main"},
		},
		heads: map[string]string{"o/web": "sha-web", "o/api": "sha-api", "o/bad": "sha-bad", "o/old": "sha-old"},
		tried: map[string]bool{"d:sha-old": true},
	}
	c := &CatchUp{Store: f, Heads: f, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	queued, err := c.Run(t.Context())
	if !slices.Equal(queued, []string{"op-a", "op-b"}) {
		t.Fatalf("queued = %v", queued)
	}
	if err == nil || !strings.Contains(err.Error(), "gone: read the head of main: branch not found") || !strings.Contains(err.Error(), "bad: queue sha-bad: db down") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(f.enqueued, []string{"a:main:sha-web", "b:dev:sha-api", "d:main:sha-old"}) {
		t.Fatalf("enqueued = %v", f.enqueued)
	}
	if !slices.Contains(f.asked, "o/api#7") || !slices.Contains(f.asked, "o/web#0") {
		t.Fatalf("heads asked = %v", f.asked)
	}

	f.listErr = errors.New("db down")
	if _, err := c.Run(t.Context()); err == nil {
		t.Fatal("a failed list was not reported")
	}
}
