package main

import (
	"bytes"
	"log/slog"
	"os"
	"os/user"
	"strconv"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/config"
)

// P5.8b: Caddy's admin socket gets a group of its own, which the worker
// must be in; without one on the host, the worker's group, with a warning.
func TestAdminGroup(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	gid, err := adminGroup(config.Caddy{Group: "shipyard-no-such-group"}, log)
	if err != nil || gid != os.Getegid() || !strings.Contains(logs.String(), "does not exist") {
		t.Errorf("missing default group: %d, %v, logs %q", gid, err, logs.String())
	}
	if _, err := adminGroup(config.Caddy{Group: "shipyard-no-such-group", GroupRequired: true}, log); err == nil {
		t.Error("a missing group set explicitly was accepted")
	}

	if os.Getegid() != 0 {
		// A group that exists but the worker is not in.
		if _, err := adminGroup(config.Caddy{Group: "root"}, log); err == nil || !strings.Contains(err.Error(), "not in group root") {
			t.Errorf("group root: %v", err)
		}
	}

	// A supplementary group the process has works.
	groups, _ := os.Getgroups()
	for _, g := range groups {
		if g == os.Getegid() || g == 0 {
			continue
		}
		grp, err := user.LookupGroupId(strconv.Itoa(g))
		if err != nil {
			continue
		}
		got, err := adminGroup(config.Caddy{Group: grp.Name, GroupRequired: true}, log)
		if err != nil || got != g {
			t.Errorf("group %s: %d, %v", grp.Name, got, err)
		}
		return
	}
	t.Log("no supplementary group to try")
}

func TestEdgeSpecGroups(t *testing.T) {
	cfg := config.Worker{Caddy: config.Caddy{Name: "shipyard-caddy", AdminDir: "/run/shipyard/caddy"}}
	cfg.Listen = "unix:/run/shipyard-api/api.sock"
	cfg.APIHostname = "shipyard.example.com"
	other := os.Getegid() + 1000
	if s := edgeSpec(cfg, other); s.GID != other || s.APIGID != os.Getegid() || s.APISocketDir != "/run/shipyard-api" {
		t.Errorf("own admin group: %+v", s)
	}
	if s := edgeSpec(cfg, os.Getegid()); s.APIGID != 0 {
		t.Errorf("fallback group: APIGID %d", s.APIGID)
	}
	cfg.APIHostname = ""
	if s := edgeSpec(cfg, other); s.APIGID != 0 || s.APISocketDir != "" {
		t.Errorf("no API hostname: %+v", s)
	}
}
