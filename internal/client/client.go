// Package client is a typed HTTP client for shipyard-api, used by the CLI.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const requestTimeout = 30 * time.Second

// Client calls one Shipyard API with one token.
type Client struct {
	base   string // scheme://host, without a trailing slash
	token  string
	http   *http.Client
	stream *http.Client // no overall timeout: event streams are long-lived
}

// New returns a client for rawURL, which is https://host[:port], http:// on a
// loopback address, or unix:///path/to/api.sock for use on the server.
// Plain HTTP to any other host is refused: the token would travel in clear.
func New(rawURL, token string) (*Client, error) {
	if token == "" {
		return nil, errors.New("no API token configured; run `shipyard login`")
	}
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || u.Scheme == "" {
		return nil, fmt.Errorf("invalid API URL %q", rawURL)
	}
	c := &Client{token: token, http: &http.Client{Timeout: requestTimeout}}
	switch u.Scheme {
	case "https":
		c.base = "https://" + u.Host + u.Path
	case "http":
		if !isLoopback(u.Hostname()) {
			return nil, fmt.Errorf("refusing plain http to %s: use https, or a loopback or unix:// address", u.Host)
		}
		c.base = "http://" + u.Host + u.Path
	case "unix":
		sock := u.Path
		if sock == "" {
			return nil, fmt.Errorf("invalid unix socket URL %q", rawURL)
		}
		c.base = "http://shipyard"
		c.http.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}
	default:
		return nil, fmt.Errorf("unsupported API URL scheme %q (want https, http on loopback, or unix)", u.Scheme)
	}
	c.stream = &http.Client{Transport: c.http.Transport}
	return c, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// FieldError is one invalid field reported by the API.
type FieldError struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// Error is an API error response (RFC 9457 problem details).
type Error struct {
	Status int          `json:"status"`
	Title  string       `json:"title"`
	Detail string       `json:"detail"`
	Fields []FieldError `json:"errors"`
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s", e.Status, e.Title)
	if e.Detail != "" {
		b.WriteString(": " + e.Detail)
	}
	for _, f := range e.Fields {
		fmt.Fprintf(&b, "\n  %s: %s", f.Field, f.Detail)
	}
	return b.String()
}

// do sends a request and decodes a JSON response into out (if not nil). It
// returns the HTTP status.
func (c *Client) do(ctx context.Context, method, path string, body any, header http.Header, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 400 {
		apiErr := &Error{Status: res.StatusCode, Title: http.StatusText(res.StatusCode)}
		_ = json.Unmarshal(data, apiErr) // a non-JSON body keeps the status text
		return res.StatusCode, apiErr
	}
	if out != nil && res.StatusCode != http.StatusNoContent {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return res.StatusCode, nil
}

func p(parts ...string) string {
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return "/" + strings.Join(parts, "/")
}

// Whoami describes the configured token.
type Whoami struct {
	Token     string     `json:"token"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (c *Client) Whoami(ctx context.Context) (Whoami, error) {
	var w Whoami
	_, err := c.do(ctx, "GET", "/v1/whoami", nil, nil, &w)
	return w, err
}

// App mirrors the API's app representation.
type App struct {
	ID             string    `json:"id"`
	Slug           string    `json:"slug"`
	Repo           string    `json:"repo"`
	Branch         string    `json:"branch"`
	DockerfilePath string    `json:"dockerfile_path"`
	BuildContext   string    `json:"build_context"`
	Port           int       `json:"port"`
	HealthPath     string    `json:"health_path"`
	HealthTimeout  string    `json:"health_timeout"`
	CPULimit       float64   `json:"cpu_limit"`
	MemoryLimit    int64     `json:"memory_limit"`
	StopTimeout    string    `json:"stop_timeout"`
	AutoDeploy     bool      `json:"auto_deploy"`
	CreatedAt      time.Time `json:"created_at"`
}

// NewApp is the create request; zero optional fields are omitted so the
// server applies its defaults.
type NewApp struct {
	Slug           string `json:"slug"`
	Repo           string `json:"repo"`
	Branch         string `json:"branch"`
	Port           int    `json:"port"`
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	BuildContext   string `json:"build_context,omitempty"`
	HealthPath     string `json:"health_path,omitempty"`
}

func (c *Client) CreateApp(ctx context.Context, n NewApp) (App, error) {
	var a App
	_, err := c.do(ctx, "POST", "/v1/apps", n, nil, &a)
	return a, err
}

func (c *Client) ListApps(ctx context.Context) ([]App, error) {
	var out struct {
		Apps []App `json:"apps"`
	}
	_, err := c.do(ctx, "GET", "/v1/apps", nil, nil, &out)
	return out.Apps, err
}

func (c *Client) GetApp(ctx context.Context, app string) (App, error) {
	var a App
	_, err := c.do(ctx, "GET", p("v1", "apps", app), nil, nil, &a)
	return a, err
}

// Env lists an app's environment keys; values are never returned.
type Env struct {
	Revision int `json:"revision"`
	Vars     []struct {
		Key    string `json:"key"`
		Secret bool   `json:"secret"`
	} `json:"vars"`
}

func (c *Client) ListEnv(ctx context.Context, app string) (Env, error) {
	var e Env
	_, err := c.do(ctx, "GET", p("v1", "apps", app, "env"), nil, nil, &e)
	return e, err
}

func (c *Client) SetEnv(ctx context.Context, app, key, value string, secret bool) (Env, error) {
	var e Env
	body := map[string]any{"value": value, "secret": secret}
	_, err := c.do(ctx, "PUT", p("v1", "apps", app, "env", key), body, nil, &e)
	return e, err
}

func (c *Client) UnsetEnv(ctx context.Context, app, key string) (Env, error) {
	var e Env
	_, err := c.do(ctx, "DELETE", p("v1", "apps", app, "env", key), nil, nil, &e)
	return e, err
}

// Domain is a hostname routed to an app.
type Domain struct {
	Hostname     string     `json:"hostname"`
	DeploymentID *string    `json:"deployment_id"`
	DNSCheckedAt *time.Time `json:"dns_checked_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (c *Client) ListDomains(ctx context.Context, app string) ([]Domain, error) {
	var out struct {
		Domains []Domain `json:"domains"`
	}
	_, err := c.do(ctx, "GET", p("v1", "apps", app, "domains"), nil, nil, &out)
	return out.Domains, err
}

func (c *Client) AddDomain(ctx context.Context, app, hostname string) (Domain, error) {
	var d Domain
	_, err := c.do(ctx, "POST", p("v1", "apps", app, "domains"), map[string]string{"hostname": hostname}, nil, &d)
	return d, err
}

func (c *Client) RemoveDomain(ctx context.Context, app, hostname string) error {
	_, err := c.do(ctx, "DELETE", p("v1", "apps", app, "domains", hostname), nil, nil, nil)
	return err
}

// Operation mirrors the API's operation representation.
type Operation struct {
	ID          string          `json:"id"`
	AppID       string          `json:"app_id"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	Phase       string          `json:"phase"`
	Payload     json.RawMessage `json:"payload"`
	Attempt     int             `json:"attempt"`
	MaxAttempts int             `json:"max_attempts"`
	LastError   string          `json:"last_error"`
	CreatedAt   time.Time       `json:"created_at"`
	FinishedAt  *time.Time      `json:"finished_at"`
}

// DeployResult is the answer to a deploy request.
type DeployResult struct {
	Operation  Operation `json:"operation"`
	Created    bool      `json:"created"`
	Superseded []string  `json:"superseded"`
}

// Deploy requests a deploy of app at ref (empty: the tracked branch's head).
// Repeating a call with the same idempotencyKey returns the first result.
func (c *Client) Deploy(ctx context.Context, app, ref, idempotencyKey string) (DeployResult, error) {
	var body any
	if ref != "" {
		body = map[string]string{"ref": ref}
	}
	h := http.Header{}
	if idempotencyKey != "" {
		h.Set("Idempotency-Key", idempotencyKey)
	}
	var r DeployResult
	_, err := c.do(ctx, "POST", p("v1", "apps", app, "deployments"), body, h, &r)
	return r, err
}

func (c *Client) Operation(ctx context.Context, id string) (Operation, error) {
	var o Operation
	_, err := c.do(ctx, "GET", p("v1", "operations", id), nil, nil, &o)
	return o, err
}
