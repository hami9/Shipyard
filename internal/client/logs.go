package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// LogLine is one line of an app's output; the server redacted known secret
// values (ADR-0008).
type LogLine struct {
	TS     time.Time `json:"ts"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
}

// Logs streams the last tail lines of the app's active container, and with
// follow, new ones until the container stops, calling fn for each. It
// returns why the stream ended. Logs are not resumable: a dropped
// connection is an error, not a retry.
func (c *Client) Logs(ctx context.Context, app string, tail int, follow bool, fn func(LogLine)) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	path := p("v1", "apps", app, "logs")
	q := url.Values{"tail": {strconv.Itoa(tail)}, "follow": {strconv.FormatBool(follow)}}
	body, err := c.openStream(ctx, path+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	defer body.Close()
	var reason string
	done, _, err := readSSE(body, cancel, nil, func(ev sseEvent) (bool, error) {
		if ev.name == "end" {
			var end struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(ev.data), &end); err != nil {
				return false, fmt.Errorf("decode end event: %w", err)
			}
			reason = end.Reason
			return true, nil
		}
		var l LogLine
		if err := json.Unmarshal([]byte(ev.data), &l); err != nil {
			return false, fmt.Errorf("decode log line: %w", err)
		}
		fn(l)
		return false, nil
	})
	switch {
	case done:
		return reason, nil
	case ctx.Err() != nil && err == nil:
		return "", fmt.Errorf("read %s: %w", path, ctx.Err())
	case err != nil:
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return "", fmt.Errorf("read %s: the stream closed before it ended", path)
}
