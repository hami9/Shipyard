package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// DomainStore is what the domain endpoints need from persistence.
type DomainStore interface {
	CreateRoute(ctx context.Context, n store.NewRoute, upstream func(deploymentID string) string) (store.Route, error)
	RoutesByApp(ctx context.Context, appID string) ([]store.Route, error)
	DeleteRoute(ctx context.Context, appID, hostname string) error
}

// Resolver looks up a hostname's addresses; *net.Resolver implements it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DomainPolicy decides which hostnames may be added (ADR-0003).
type DomainPolicy struct {
	// Preflight requires every A/AAAA record to be one of PublicIPs.
	Preflight bool
	PublicIPs []netip.Addr
	// Suffixes, if set, is the allow-list of domains hostnames must be under.
	Suffixes []string
}

const dnsTimeout = 5 * time.Second

type domainHandlers struct {
	*appHandlers
	routes   DomainStore
	resolver Resolver
	policy   DomainPolicy
}

// domainJSON is a hostname as the API returns it. The API only records it;
// the worker loads Caddy from the routes table on its next sync.
type domainJSON struct {
	Hostname     string     `json:"hostname"`
	DeploymentID *string    `json:"deployment_id"` // null: served "no active deployment" until the next deploy
	DNSCheckedAt *time.Time `json:"dns_checked_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

func toDomainJSON(r store.Route) domainJSON {
	return domainJSON{r.Hostname, r.DeploymentID, r.DNSCheckedAt, r.CreatedAt}
}

func (h *domainHandlers) list(w http.ResponseWriter, r *http.Request) {
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	rows, err := h.routes.RoutesByApp(r.Context(), a.ID)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	out := make([]domainJSON, len(rows))
	for i, row := range rows {
		out[i] = toDomainJSON(row)
	}
	writeJSON(w, http.StatusOK, "application/json", struct {
		Domains []domainJSON `json:"domains"`
	}{out})
}

func (h *domainHandlers) add(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostname *string `json:"hostname"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	if req.Hostname == nil {
		writeError(w, r, h.log, app.FieldErrors{{Field: "hostname", Detail: "is required"}})
		return
	}
	host, err := app.NormalizeHostname(*req.Hostname)
	if err != nil {
		writeError(w, r, h.log, app.FieldErrors{{Field: "hostname", Detail: err.Error()}})
		return
	}
	if !app.SuffixAllowed(host, h.policy.Suffixes) {
		writeError(w, r, h.log, app.FieldErrors{{Field: "hostname",
			Detail: "is not under an allowed domain (" + strings.Join(h.policy.Suffixes, ", ") + ")"}})
		return
	}
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	n := store.NewRoute{AppID: a.ID, Hostname: host}
	if h.policy.Preflight {
		if ok := h.preflight(w, r, host); !ok {
			return
		}
		now := time.Now()
		n.DNSCheckedAt = &now
	}
	route, err := h.routes.CreateRoute(r.Context(), n, func(dep string) string { return app.Upstream(a.Slug, dep, a.InternalPort) })
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, "application/json", toDomainJSON(route))
}

// preflight refuses a hostname whose DNS does not point only at this server,
// before any certificate is requested: failed validations count against
// Let's Encrypt's limits [LE-LIMITS][CADDY-HTTPS].
func (h *domainHandlers) preflight(w http.ResponseWriter, r *http.Request, host string) bool {
	if len(h.policy.PublicIPs) == 0 {
		writeProblem(w, http.StatusServiceUnavailable,
			"DNS preflight is on but the server's public IPs are not configured (SHIPYARD_PUBLIC_IPS)")
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), dnsTimeout)
	defer cancel()
	addrs, err := h.resolver.LookupNetIP(ctx, "ip", host)
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound, err == nil && len(addrs) == 0:
		writeError(w, r, h.log, app.FieldErrors{{Field: "hostname", Detail: "has no A or AAAA record; point it at " + ips(h.policy.PublicIPs) + " first"}})
		return false
	case err != nil:
		h.log.WarnContext(r.Context(), "DNS preflight lookup failed", "err", err)
		writeProblem(w, http.StatusServiceUnavailable, "DNS lookup failed; try again")
		return false
	}
	if ok, foreign := app.PointsHere(addrs, h.policy.PublicIPs); !ok {
		writeError(w, r, h.log, app.FieldErrors{{Field: "hostname",
			Detail: fmt.Sprintf("resolves to %s, which is not this server (%s)", ips(foreign), ips(h.policy.PublicIPs))}})
		return false
	}
	return true
}

func (h *domainHandlers) remove(w http.ResponseWriter, r *http.Request) {
	host, err := app.NormalizeHostname(r.PathValue("hostname"))
	if err != nil {
		writeError(w, r, h.log, store.ErrNotFound)
		return
	}
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	if err := h.routes.DeleteRoute(r.Context(), a.ID, host); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func ips(addrs []netip.Addr) string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}
