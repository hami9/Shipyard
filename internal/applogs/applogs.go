// Package applogs carries app container logs from the worker, which reads
// Docker, to the API, which never does (invariant 1, ADR-0008). The worker
// serves Server on a private Unix socket; the API reads it with Client.
//
// The protocol is one JSON object per line: log lines, then a final line
// with only "end" set, so a reader can tell a finished stream from a broken
// one.
package applogs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// Tail bounds (ADR-0008).
const (
	DefaultTail = 100
	MaxTail     = 1000
)

// Line is one log line, or with End set, the end of the stream.
type Line struct {
	TS     time.Time `json:"ts,omitzero"`
	Stream string    `json:"stream,omitempty"` // stdout or stderr
	Text   string    `json:"line,omitempty"`
	End    string    `json:"end,omitempty"` // why the stream ended
}

// Why a stream ends.
const (
	EndTail    = "end of the requested lines"
	EndStopped = "the container stopped"
	EndFailed  = "reading the logs failed"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Store finds the container to read: the app's active deployment's.
type Store interface {
	ActiveDeployment(ctx context.Context, appID string) (store.Deployment, error)
}

// Secrets decrypts a revision's secret values, for redaction.
type Secrets interface {
	SecretValues(ctx context.Context, revisionID string) ([]string, error)
}

// Source reads a container's output (internal/runtime).
type Source interface {
	StreamLogs(ctx context.Context, containerID string, tail int, follow bool, fn func(Line) error) error
}

// Server is the worker's side. It only reads: nothing on the socket starts,
// stops, or changes anything.
type Server struct {
	Store   Store
	Secrets Secrets
	Source  Source
	Log     *slog.Logger
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/logs" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	appID := q.Get("app")
	tail, err := ParseTail(q.Get("tail"))
	follow := q.Get("follow") == "true"
	if !uuidRE.MatchString(appID) || err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	// The database names the container (invariant 2); nothing else is read.
	dep, err := s.Store.ActiveDeployment(ctx, appID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && dep.ContainerID == "") {
		http.Error(w, "no active deployment", http.StatusNotFound)
		return
	}
	if err != nil {
		s.Log.Warn("logs: active deployment lookup failed", slog.String("app_id", appID), slog.Any("err", err))
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	redactor := app.NewRedactor(nil)
	if dep.EnvRevisionID != nil {
		secrets, err := s.Secrets.SecretValues(ctx, *dep.EnvRevisionID)
		if err != nil { // never stream what cannot be redacted
			s.Log.Warn("logs: cannot read secrets to redact", slog.String("app_id", appID), slog.Any("err", err))
			http.Error(w, "cannot redact", http.StatusInternalServerError)
			return
		}
		redactor = app.NewRedactor(secrets)
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc, rc := json.NewEncoder(w), http.NewResponseController(w)
	err = s.Source.StreamLogs(ctx, dep.ContainerID, tail, follow, func(l Line) error {
		l.Text = redactor.Redact(l.Text)
		if err := enc.Encode(l); err != nil {
			return err
		}
		return rc.Flush()
	})
	end := EndTail
	switch {
	case ctx.Err() != nil:
		return // the reader left
	case err != nil:
		s.Log.Warn("logs: stream failed", slog.String("app_id", appID), slog.String("deployment_id", dep.ID), slog.Any("err", err))
		end = EndFailed
	case follow:
		end = EndStopped
	}
	enc.Encode(Line{End: end})
	rc.Flush()
}

// ParseTail reads a tail count: empty is DefaultTail, otherwise 0..MaxTail.
func ParseTail(s string) (int, error) {
	if s == "" {
		return DefaultTail, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > MaxTail {
		return 0, fmt.Errorf("must be a number from 0 to %d", MaxTail)
	}
	return n, nil
}

// Errors a Client reports.
var (
	ErrUnavailable  = errors.New("the worker's log service is not reachable")
	ErrNoDeployment = errors.New("no active deployment")
)

// Client is the API's side.
type Client struct{ http *http.Client }

// NewClient reads the worker's socket at path.
func NewClient(path string) *Client {
	return &Client{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}}
}

// Stream is an open log stream; Next returns lines until one with End set.
type Stream struct {
	body io.ReadCloser
	dec  *json.Decoder
}

// Logs opens the app's log stream.
func (c *Client) Logs(ctx context.Context, appID string, tail int, follow bool) (*Stream, error) {
	q := url.Values{"app": {appID}, "tail": {strconv.Itoa(tail)}, "follow": {strconv.FormatBool(follow)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://worker/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	switch res.StatusCode {
	case http.StatusOK:
		return &Stream{body: res.Body, dec: json.NewDecoder(res.Body)}, nil
	case http.StatusNotFound:
		res.Body.Close()
		return nil, ErrNoDeployment
	default:
		res.Body.Close()
		return nil, fmt.Errorf("worker log service: %s", res.Status)
	}
}

// Next returns the next line. A stream that stops without an end line (the
// worker restarted) returns io.ErrUnexpectedEOF.
func (s *Stream) Next() (Line, error) {
	var l Line
	if err := s.dec.Decode(&l); err != nil {
		if errors.Is(err, io.EOF) {
			return l, io.ErrUnexpectedEOF
		}
		return l, err
	}
	return l, nil
}

// Close ends the stream; a blocked Next returns.
func (s *Stream) Close() error { return s.body.Close() }
