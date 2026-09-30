package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Event is one line of an operation's log.
type Event struct {
	Seq     int64     `json:"seq"`
	TS      time.Time `json:"ts"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Stream limits. The server sends a keepalive every 15 s, so a silent
// connection is dead after three missed ones [WHATWG-SSE].
const (
	streamIdle      = 45 * time.Second
	streamRetry     = 2 * time.Second
	streamAttempts  = 5       // reconnects in a row without receiving anything
	maxStreamLine   = 1 << 20 // a 16 KiB message, JSON-escaped, fits easily
	eventStreamType = "text/event-stream"
)

// FollowEvents streams an operation's events after seq after (0: from the
// start), calling fn for each, until the operation ends; it returns the
// finished operation. A dropped connection is resumed with Last-Event-ID,
// so no event is lost or repeated.
func (c *Client) FollowEvents(ctx context.Context, id string, after int64, fn func(Event)) (Operation, error) {
	retry, failures := streamRetry, 0
	for {
		op, done, progress, err := c.followOnce(ctx, id, &after, &retry, fn)
		switch {
		case done:
			return op, nil
		case ctx.Err() != nil:
			return Operation{}, ctx.Err()
		case errors.As(err, new(*Error)):
			return Operation{}, err // an API answer, such as 404: retrying will not help
		}
		if progress {
			failures = 0
		}
		if failures++; failures > streamAttempts {
			return Operation{}, fmt.Errorf("event stream: %w", err)
		}
		select {
		case <-ctx.Done():
			return Operation{}, ctx.Err()
		case <-time.After(retry):
		}
	}
}

// followOnce reads one connection. It reports whether the end event arrived
// and whether an event or a keepalive did, which resets the retry budget.
func (c *Client) followOnce(ctx context.Context, id string, after *int64, retry *time.Duration, fn func(Event)) (op Operation, done, progress bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	path := p("v1", "operations", id, "events")
	h := http.Header{}
	if *after > 0 {
		h.Set("Last-Event-ID", strconv.FormatInt(*after, 10))
	}
	body, err := c.openStream(ctx, path, h)
	if err != nil {
		return op, false, false, err
	}
	defer body.Close()
	done, progress, err = readSSE(body, cancel, retry, func(ev sseEvent) (bool, error) {
		if ev.name == "end" {
			if err := json.Unmarshal([]byte(ev.data), &op); err != nil {
				return false, fmt.Errorf("decode end event: %w", err)
			}
			return true, nil
		}
		var e Event
		if err := json.Unmarshal([]byte(ev.data), &e); err != nil {
			return false, fmt.Errorf("decode event: %w", err)
		}
		fn(e)
		if n, err := strconv.ParseInt(ev.id, 10, 64); err == nil {
			*after = n
		}
		return false, nil
	})
	switch {
	case done:
		return op, true, true, nil
	case err != nil:
		return op, false, progress, fmt.Errorf("read %s: %w", path, err)
	}
	return op, false, progress, fmt.Errorf("read %s: stream closed before the operation ended", path)
}

// openStream starts a GET of an SSE stream; an API error is returned as
// *Error.
func (c *Client) openStream(ctx context.Context, path string, h http.Header) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range h {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", eventStreamType)
	res, err := c.stream.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if res.StatusCode >= 400 {
		defer res.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		apiErr := &Error{Status: res.StatusCode, Title: http.StatusText(res.StatusCode)}
		_ = json.Unmarshal(data, apiErr)
		return nil, apiErr
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, eventStreamType) {
		res.Body.Close()
		return nil, fmt.Errorf("GET %s: unexpected content type %q", path, ct)
	}
	return res.Body, nil
}

type sseEvent struct{ name, id, data string }

// readSSE dispatches the events of body to fn until fn reports done, fails,
// or the stream ends [WHATWG-SSE]. A stream silent past streamIdle is dead:
// cancel drops it. It reports whether an event or a keepalive arrived.
func readSSE(body io.Reader, cancel func(), retry *time.Duration, fn func(sseEvent) (bool, error)) (done, progress bool, err error) {
	idle := time.AfterFunc(streamIdle, cancel)
	defer idle.Stop()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), maxStreamLine)
	var ev sseEvent
	for sc.Scan() {
		idle.Reset(streamIdle)
		line := sc.Text()
		if line == "" { // dispatch
			if ev.data != "" || ev.name != "" {
				progress = true
				if done, err := fn(ev); done || err != nil {
					return done, true, err
				}
			}
			ev.name, ev.data = "", "" // the id carries over, as in EventSource
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "": // a comment: the keepalive shows the connection is healthy
			progress = true
		case "event":
			ev.name = value
		case "data":
			if ev.data != "" {
				ev.data += "\n"
			}
			ev.data += value
		case "id":
			ev.id = value
		case "retry":
			if ms, err := strconv.Atoi(value); err == nil && ms > 0 && retry != nil {
				*retry = time.Duration(ms) * time.Millisecond
			}
		}
	}
	return false, progress, sc.Err()
}
