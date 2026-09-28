package main

import (
	"errors"
	"strings"
	"testing"
)

func TestEdgeGID(t *testing.T) {
	lookup := func(name string) (string, error) {
		switch name {
		case "shipyard-edge":
			return "990", nil
		case "shipyard":
			return "989", nil
		case "root":
			return "0", nil
		case "odd":
			return "S-1-5-32", nil
		}
		return "", errors.New("unknown group")
	}
	const egid = 989 // the worker's primary group, shared with the API
	groups := []int{989, 990, 991}

	for group, want := range map[string]int{"shipyard-edge": 990, "990": 990, "991": 991} {
		if got, err := edgeGID(group, lookup, egid, groups); err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", group, got, err, want)
		}
	}
	for name, tc := range map[string]struct{ group, want string }{
		"primary group by name": {"shipyard", "primary group"},
		"primary group by gid":  {"989", "primary group"},
		"not a member":          {"992", "not in group"},
		"root by name":          {"root", "root"},
		"root by gid":           {"0", "root"},
		"unknown name":          {"nobody-here", "unknown group"},
		"non-numeric gid":       {"odd", "has gid"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := edgeGID(tc.group, lookup, egid, groups)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "SHIPYARD_CADDY_GROUP") {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
