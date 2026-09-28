package routing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// RouteLister reads the routes table.
type RouteLister interface {
	ListRoutes(ctx context.Context) ([]store.Route, error)
}

// Verification tuning: Caddy applies a load before answering it, so a
// route that does not work within this window is broken, not slow.
const (
	verifyAttempts = 5
	verifyPause    = 500 * time.Millisecond
	verifyTimeout  = 5 * time.Second
)

// Router moves an app's traffic (ARCHITECTURE §5 step 7). Caddy's config is
// always rendered from the routes table plus the switches in progress: a
// switch points one app's routes at its candidate until the deploy releases
// it, after committing it to the table or failing. Keeping in-progress
// switches in every render stops a concurrent Sync (the reconciler) from
// reverting a candidate mid-verification, which would verify the old
// container instead. One Router serves the worker process.
type Router struct {
	routes   RouteLister
	admin    *Admin
	settings Settings
	verify   *http.Client

	mu      sync.Mutex        // serializes renders and loads
	pending map[string]string // app ID -> candidate upstream
}

// NewRouter returns a Router. Settings.VerifySocket is required.
func NewRouter(routes RouteLister, s Settings) (*Router, error) {
	if s.VerifySocket == "" {
		return nil, fmt.Errorf("%w: the router needs a verify socket", ErrInvalid)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &Router{routes: routes, admin: NewAdmin(s.AdminSocket), settings: s, pending: map[string]string{}, verify: &http.Client{
		Timeout: verifyTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", s.VerifySocket)
			},
			Proxy:             nil,
			DisableKeepAlives: true,
		},
		// A 3xx passes, as in the health gate; never follow it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Sync makes Caddy serve the routes table, keeping switches in progress
// (Reconciler step 3; worker start). It reports whether it reloaded.
func (r *Router) Sync(ctx context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, changed, err := r.load(ctx, "")
	return changed, err
}

// Switch points the app's routes at upstream, loads that, then requests
// healthPath on each of its hostnames through Caddy with that Host header.
// It returns the hostnames it verified; none means the app has no routes and
// nothing changed. Unless it returns none, the switch stays in every render
// until Release, whatever the outcome.
func (r *Router) Switch(ctx context.Context, appID, upstream, healthPath string) ([]string, error) {
	r.mu.Lock()
	r.pending[appID] = upstream
	hosts, _, err := r.load(ctx, appID)
	if len(hosts) == 0 {
		delete(r.pending, appID)
	}
	r.mu.Unlock()
	if err != nil || len(hosts) == 0 {
		return nil, err
	}
	for _, h := range hosts {
		if err := r.check(ctx, h, healthPath); err != nil {
			return nil, fmt.Errorf("verify %s through caddy: %w", h, err)
		}
	}
	return hosts, nil
}

// Release ends the app's switch and loads the table as it now is: the
// candidate if the deploy committed it, the previous target if not.
func (r *Router) Release(ctx context.Context, appID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, appID)
	_, changed, err := r.load(ctx, "")
	return changed, err
}

// load renders the routes table with the pending switches applied and loads
// it. It returns the hostnames of app (if given). Call with r.mu held.
func (r *Router) load(ctx context.Context, app string) ([]string, bool, error) {
	rows, err := r.routes.ListRoutes(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("list routes: %w", err)
	}
	routes := FromStore(rows)
	var hosts []string
	for i, row := range rows {
		if up, ok := r.pending[row.AppID]; ok {
			routes[i].Upstream = up
		}
		if app != "" && row.AppID == app {
			hosts = append(hosts, row.Hostname)
		}
	}
	if app != "" && len(hosts) == 0 {
		return nil, false, nil
	}
	changed, err := r.apply(ctx, routes)
	return hosts, changed, err
}

func (r *Router) apply(ctx context.Context, routes []Route) (bool, error) {
	cfg, err := Render(r.settings, routes)
	if err != nil {
		return false, fmt.Errorf("render caddy config: %w", err)
	}
	changed, err := r.admin.Apply(ctx, cfg)
	if err != nil {
		return false, fmt.Errorf("load caddy config: %w", err)
	}
	return changed, nil
}

// check asks Caddy's verify server for path with the hostname's Host header
// until it answers 2xx/3xx.
func (r *Router) check(ctx context.Context, host, path string) error {
	var last error
	for i := range verifyAttempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return errors.Join(ctx.Err(), last)
			case <-time.After(verifyPause):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "shipyard-verify")
		resp, err := r.verify.Do(req)
		if err != nil {
			last = err
			continue
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode <= 399 {
			return nil
		}
		last = fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return last
}
