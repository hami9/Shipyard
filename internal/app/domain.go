package app

import (
	"errors"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// hostnameRE mirrors the routes.hostname CHECK: lowercase DNS labels, at
// least two, the last one not numeric (so no IP literals), no wildcards.
var hostnameRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeHostname lowercases a hostname and drops one trailing dot, then
// checks it is a name a route can serve: one app per hostname, exact names
// only (ADR-0003).
func NormalizeHostname(s string) (string, error) {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	switch {
	case h == "":
		return "", errors.New("is required")
	case strings.HasPrefix(h, "*."):
		return "", errors.New("wildcards are not supported; add each hostname")
	case len(h) > 253 || !hostnameRE.MatchString(h):
		return "", errors.New("must be a fully qualified domain name such as app.example.com")
	}
	return h, nil
}

// SuffixAllowed reports whether host may be claimed under the allow-list:
// equal to a suffix or below it. An empty list allows any hostname.
func SuffixAllowed(host string, suffixes []string) bool {
	if len(suffixes) == 0 {
		return true
	}
	return slices.ContainsFunc(suffixes, func(s string) bool {
		return host == s || strings.HasSuffix(host, "."+s)
	})
}

// PointsHere reports whether DNS sends host to this server: at least one
// A/AAAA record, and every one of them among publicIPs. A stray record
// would send Let's Encrypt's validation elsewhere and burn its failure
// budget [CADDY-HTTPS][LE-LIMITS]. It returns the addresses that are not
// ours.
func PointsHere(addrs, publicIPs []netip.Addr) (bool, []netip.Addr) {
	var foreign []netip.Addr
	for _, a := range addrs {
		if !slices.Contains(publicIPs, a.Unmap()) {
			foreign = append(foreign, a)
		}
	}
	return len(addrs) > 0 && len(foreign) == 0, foreign
}

// ContainerName is a deployment's container name (internal/runtime uses the
// same convention; cmd/shipyard-worker's tests keep them equal). The API
// uses it to point a new hostname at the running deployment without asking
// Docker (invariant 1).
func ContainerName(slug, deploymentID string) string { return "shipyard-" + slug + "-" + deploymentID }

// Upstream is the Caddy dial address of a deployment on its app network.
func Upstream(slug, deploymentID string, port int) string {
	return net.JoinHostPort(ContainerName(slug, deploymentID), strconv.Itoa(port))
}
