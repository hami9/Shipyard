package main

import (
	"fmt"
	"os/user"
	"slices"
	"strconv"

	"github.com/hami9/shipyard/internal/config"
)

// edgeGID resolves SHIPYARD_CADDY_GROUP to the gid that owns Caddy's admin
// directory, and so its admin and verify sockets (ADR-0003). The worker
// must be in that group to use them. It must not be the worker's primary
// group: in the systemd units that group (shipyard) is shared with the API
// for the log socket (ADR-0008), and the API must not reach Caddy
// (invariants 1 and 12). lookup maps a group name to its decimal gid.
func edgeGID(group string, lookup func(name string) (string, error), egid int, groups []int) (int, error) {
	id := group
	if _, err := strconv.Atoi(group); err != nil {
		if id, err = lookup(group); err != nil {
			return 0, fmt.Errorf("%s: group %q: %w", config.EnvCaddyGroup, group, err)
		}
	}
	gid, err := strconv.Atoi(id)
	switch {
	case err != nil:
		return 0, fmt.Errorf("%s: group %q has gid %q", config.EnvCaddyGroup, group, id)
	case gid <= 0:
		return 0, fmt.Errorf("%s: group %q must not be root's", config.EnvCaddyGroup, group)
	case gid == egid:
		return 0, fmt.Errorf("%s: group %q is the worker's primary group, which the API shares; use a group only the worker has (shipyard-edge)", config.EnvCaddyGroup, group)
	case !slices.Contains(groups, gid):
		return 0, fmt.Errorf("%s: the worker is not in group %q (gid %d); add it to SupplementaryGroups=", config.EnvCaddyGroup, group, gid)
	}
	return gid, nil
}

// lookupGID reads the group database; the static binary's pure Go lookup
// parses /etc/group [GO-OSUSER].
func lookupGID(name string) (string, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return "", err
	}
	return g.Gid, nil
}
