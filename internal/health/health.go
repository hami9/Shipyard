// Package health is the worker's health gate (ARCHITECTURE §5 step 6): it
// probes a candidate's HTTP endpoint by container IP until it answers 2xx or
// 3xx several times in a row, while the container must keep running.
package health

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Defaults for Config zero values.
const (
	DefaultTimeout        = time.Minute // the apps.health_timeout default
	DefaultInterval       = time.Second
	DefaultSuccesses      = 3
	DefaultRequestTimeout = 2 * time.Second
)

// ErrUnhealthy means the timeout passed without enough consecutive passes.
var ErrUnhealthy = errors.New("health check did not pass")

// Config is one health gate.
type Config struct {
	URL            string        // http://<container-ip>:<port><path>
	Timeout        time.Duration // the app's health_timeout
	Interval       time.Duration // between probes
	Successes      int           // consecutive passes required
	RequestTimeout time.Duration // per probe
}

// Wait probes until Successes consecutive probes pass, or Timeout passes.
// alive runs before every probe; its error (the container exited or
// restarted) ends the wait at once.
func Wait(ctx context.Context, c Config, alive func(context.Context) error) error {
	c = c.withDefaults()
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	client := newClient()
	defer client.CloseIdleConnections()

	passes, last := 0, "no probe completed"
	tick := time.NewTicker(c.Interval)
	defer tick.Stop()
	for ctx.Err() == nil {
		if err := alive(ctx); err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
		}
		if err := probe(ctx, client, c); err != nil {
			passes = 0
			if ctx.Err() == nil { // a probe cut off by the deadline says nothing new
				last = err.Error()
			}
		} else if passes++; passes >= c.Successes {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
	if cause := context.Cause(ctx); !errors.Is(cause, context.DeadlineExceeded) {
		return cause // cancelled by the caller, e.g. the lease was lost
	}
	return fmt.Errorf("%w within %s: %s", ErrUnhealthy, c.Timeout, last)
}

// Probe makes one request as the gate does: 2xx or 3xx passes. The
// reconciler checks active apps with it (ADR-0013). A timeout of 0 means
// DefaultRequestTimeout.
func Probe(ctx context.Context, url string, timeout time.Duration) error {
	client := newClient()
	defer client.CloseIdleConnections()
	return probe(ctx, client, Config{URL: url, RequestTimeout: timeout}.withDefaults())
}

func newClient() *http.Client {
	return &http.Client{
		// The target is a container IP: never go through a proxy from the
		// environment, never follow redirects (a 3xx passes), and never keep
		// connections to a container that may be replaced.
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func probe(ctx context.Context, client *http.Client, c Config) error {
	ctx, cancel := context.WithTimeout(ctx, c.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "shipyard-health")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", req.URL.RequestURI(), errors.Unwrap(err))
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 399 {
		return fmt.Errorf("GET %s: status %d", req.URL.RequestURI(), resp.StatusCode)
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	if c.Successes <= 0 {
		c.Successes = DefaultSuccesses
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
	return c
}
