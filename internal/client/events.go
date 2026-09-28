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
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return op, false, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", eventStreamType)
	if *after > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(*after, 10))
	}
	res, err := c.stream.Do(req)
	if err != nil {
		return op, false, false, fmt.Errorf("GET %s: %w", path, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		apiErr := &Error{Status: res.StatusCode, Title: http.StatusText(res.StatusCode)}
		_ = json.Unmarshal(data, apiErr)
		return op, false, false, apiErr
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, eventStreamType) {
		return op, false, false, fmt.Errorf("GET %s: unexpected content type %q", path, ct)
	}

	// A connection that goes silent past the keepalive is dropped.
	idle := time.AfterFunc(streamIdle, cancel)
	defer idle.Stop()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), maxStreamLine)
	var name, data, lastID string
	for sc.Scan() {
		idle.Reset(streamIdle)
		line := sc.Text()
		if line == "" { // dispatch [WHATWG-SSE]
			if name == "end" {
				if err := json.Unmarshal([]byte(data), &op); err != nil {
					return op, false, true, fmt.Errorf("decode end event: %w", err)
				}
				return op, true, true, nil
			}
			if data != "" {
				var e Event
				if err := json.Unmarshal([]byte(data), &e); err != nil {
					return op, false, true, fmt.Errorf("decode event: %w", err)
				}
				fn(e)
				progress = true
				if n, err := strconv.ParseInt(lastID, 10, 64); err == nil {
					*after = n
				}
			}
			name, data = "", ""
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "": // a comment: the keepalive shows the connection is healthy
			progress = true
		case "event":
			name = value
		case "data":
			if data != "" {
				data += "\n"
			}
			data += value
		case "id":
			lastID = value
		case "retry":
			if ms, err := strconv.Atoi(value); err == nil && ms > 0 {
				*retry = time.Duration(ms) * time.Millisecond
			}
		}
	}
	if err := sc.Err(); err != nil {
		return op, false, progress, fmt.Errorf("read %s: %w", path, err)
	}
	return op, false, progress, fmt.Errorf("read %s: stream closed before the operation ended", path)
}
