package routing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
// always rendered from the routes table; a switch renders it with one app's
// routes pointing at a candidate, which the caller commits to the table only
// after the switch verified.
type Router struct {
	routes   RouteLister
	admin    *Admin
	settings Settings
	verify   *http.Client
}

// NewRouter returns a Router. Settings.VerifySocket is required.
func NewRouter(routes RouteLister, s Settings) (*Router, error) {
	if s.VerifySocket == "" {
		return nil, fmt.Errorf("%w: the router needs a verify socket", ErrInvalid)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &Router{routes: routes, admin: NewAdmin(s.AdminSocket), settings: s, verify: &http.Client{
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

// Restore makes Caddy serve exactly the routes table (Reconciler step 3,
// and every failed switch). It reports whether it reloaded.
func (r *Router) Restore(ctx context.Context) (bool, error) {
	rows, err := r.routes.ListRoutes(ctx)
	if err != nil {
		return false, fmt.Errorf("list routes: %w", err)
	}
	return r.apply(ctx, FromStore(rows))
}

// Switch loads a config in which the app's routes point at upstream, then
// requests healthPath on each of its hostnames through Caddy with that Host
// header. It returns the hostnames it verified; none means the app has no
// routes and nothing was loaded. On error, Caddy may be serving the
// candidate: the caller must Restore.
func (r *Router) Switch(ctx context.Context, appID, upstream, healthPath string) ([]string, error) {
	rows, err := r.routes.ListRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	routes := FromStore(rows)
	var hosts []string
	for i, row := range rows {
		if row.AppID == appID {
			routes[i].Upstream = upstream
			hosts = append(hosts, row.Hostname)
		}
	}
	if len(hosts) == 0 {
		return nil, nil
	}
	if _, err := r.apply(ctx, routes); err != nil {
		return nil, err
	}
	for _, h := range hosts {
		if err := r.check(ctx, h, healthPath); err != nil {
			return nil, fmt.Errorf("verify %s through caddy: %w", h, err)
		}
	}
	return hosts, nil
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
