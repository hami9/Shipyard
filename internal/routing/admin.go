package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// ErrConflict means Caddy's config changed since it was read (HTTP 412 on
// If-Match) [CADDY-API]. Read it again and retry.
var ErrConflict = errors.New("caddy config changed concurrently")

// LoadError is Caddy refusing a request, e.g. an invalid config. Caddy keeps
// its previous config when a load fails [CADDY-API], so the serving route
// is unchanged (invariant 5).
type LoadError struct {
	Status  int
	Message string
}

func (e *LoadError) Error() string {
	return fmt.Sprintf("caddy admin: HTTP %d: %s", e.Status, e.Message)
}

const (
	// applyAttempts bounds the read-compare-load loop under concurrent changes.
	applyAttempts = 3
	// requestTimeout applies when the caller's context has no deadline; a
	// load blocks until Caddy's reload completes [CADDY-API].
	requestTimeout = time.Minute
	maxResponse    = 8 << 20
)

// Admin talks to Caddy's admin API over its Unix socket (invariant 12).
// Only the worker holds one.
type Admin struct {
	socket string
	hc     *http.Client
}

// NewAdmin returns a client for the admin socket at path.
func NewAdmin(socket string) *Admin {
	return &Admin{socket: socket, hc: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
		// The socket is local; no proxy may ever see the config.
		Proxy: nil,
	}}}
}

// Config returns Caddy's whole running config and its Etag.
func (a *Admin) Config(ctx context.Context) ([]byte, string, error) {
	resp, body, err := a.do(ctx, http.MethodGet, "/config/", nil, "")
	if err != nil {
		return nil, "", err
	}
	etag := resp.Header.Get("Etag")
	if etag == "" {
		return nil, "", errors.New("caddy admin: GET /config/ returned no Etag")
	}
	return body, etag, nil
}

// Load replaces the whole config. With a non-empty etag it is conditional:
// if the config changed since that Etag was read, it fails with ErrConflict
// and nothing changes.
//
// It posts to /config/, not /load: Caddy checks If-Match only on /config/
// paths, and /load ignores it (Caddy 2.11.4 source, observed) [CADDY-ADMIN-SRC].
// Both replace and reload the config the same way, restoring the previous
// one if the new one fails.
func (a *Admin) Load(ctx context.Context, cfg []byte, etag string) error {
	_, _, err := a.do(ctx, http.MethodPost, "/config/", cfg, etag)
	return err
}

// Apply makes Caddy run cfg. It reads the running config with its Etag,
// returns without a reload if they are already equal (a pure re-render is
// free), and otherwise loads cfg conditionally, re-reading after a 412. It
// reports whether it loaded.
func (a *Admin) Apply(ctx context.Context, cfg []byte) (bool, error) {
	for range applyAttempts {
		current, etag, err := a.Config(ctx)
		if err != nil {
			return false, err
		}
		same, err := sameJSON(current, cfg)
		if err != nil {
			return false, err
		}
		if same {
			return false, nil
		}
		err = a.Load(ctx, cfg, etag)
		if !errors.Is(err, ErrConflict) {
			return err == nil, err
		}
	}
	return false, fmt.Errorf("caddy admin: gave up after %d attempts: %w", applyAttempts, ErrConflict)
}

func (a *Admin) do(ctx context.Context, method, path string, body []byte, ifMatch string) (*http.Response, []byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	// The host is ignored on a Unix socket; the path is all that matters.
	req, err := http.NewRequestWithContext(ctx, method, "http://caddy"+path, rd)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("caddy admin %s %s via %s: %w", method, path, a.socket, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, nil, fmt.Errorf("caddy admin %s %s: %w", method, path, err)
	}
	switch {
	case resp.StatusCode == http.StatusPreconditionFailed:
		return nil, nil, fmt.Errorf("caddy admin %s %s: %w", method, path, ErrConflict)
	case resp.StatusCode != http.StatusOK:
		return nil, nil, &LoadError{Status: resp.StatusCode, Message: caddyMessage(out)}
	}
	return resp, out, nil
}

// caddyMessage extracts Caddy's {"error": "..."} body, bounded for logs.
func caddyMessage(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		msg = e.Error
	}
	if len(msg) > 2000 {
		msg = msg[:2000] + "…"
	}
	return msg
}

// sameJSON compares configs semantically: Caddy returns its stored config
// re-encoded, so bytes differ while the meaning is equal.
func sameJSON(a, b []byte) (bool, error) {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false, fmt.Errorf("caddy admin: running config: %w", err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false, fmt.Errorf("caddy admin: new config: %w", err)
	}
	return reflect.DeepEqual(x, y), nil
}
