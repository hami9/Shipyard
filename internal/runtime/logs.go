package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// MaxLogLine bounds one streamed log line; the rest of a longer line is
// dropped and the line is marked. The bound includes room for the longest
// timestamp, so a line keeps a few bytes more when its timestamp is short.
const MaxLogLine = 16 << 10

const truncatedLine = " …[truncated]"

// LogLine is one line of a container's output.
type LogLine struct {
	TS     time.Time
	Stream string // stdout or stderr
	Text   string
}

// errStopStream ends StreamLogs early when fn fails; fn's error is returned.
var errStopStream = errors.New("stop")

// StreamLogs calls fn for each of the last tail lines of a managed
// container's stdout and stderr, then, with follow, for every new line until
// the container stops or ctx ends [DK-LOGS]. An error from fn stops it.
func (r *Runtime) StreamLogs(ctx context.Context, id string, tail int, follow bool, fn func(LogLine) error) error {
	if _, err := r.managed(ctx, id); err != nil {
		return err
	}
	rc, err := r.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: follow, Tail: strconv.Itoa(max(tail, 0)),
	})
	if err != nil {
		return fmt.Errorf("logs of container %s: %w", id, wrap(err))
	}
	defer rc.Close()
	var fnErr error
	emit := func(l LogLine) error {
		if fnErr = fn(l); fnErr != nil {
			return errStopStream
		}
		return nil
	}
	stdout, stderr := &lineWriter{stream: "stdout", emit: emit}, &lineWriter{stream: "stderr", emit: emit}
	// Containers never get a TTY, so the stream is multiplexed [MOBY-CLIENT].
	_, err = stdcopy.StdCopy(stdout, stderr, rc)
	if fnErr != nil {
		return fnErr
	}
	for _, w := range []*lineWriter{stdout, stderr} {
		if err := w.flush(); err != nil {
			return fnErr
		}
	}
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("logs of container %s: %w", id, err)
	}
	return ctx.Err()
}

// lineWriter splits one stream's frames into timestamped lines. Docker
// frames need not end at a newline, so a partial line waits for the next.
type lineWriter struct {
	stream  string
	emit    func(LogLine) error
	buf     []byte
	dropped bool // the current line went past MaxLogLine
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.add(p)
			break
		}
		w.add(p[:i])
		p = p[i+1:]
		if err := w.line(); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// add keeps up to the timestamp, its space, and MaxLogLine of text.
func (w *lineWriter) add(p []byte) {
	room := len(time.RFC3339Nano) + 1 + MaxLogLine - len(w.buf)
	if len(p) > room {
		p, w.dropped = p[:max(room, 0)], true
	}
	w.buf = append(w.buf, p...)
}

func (w *lineWriter) line() error {
	raw, dropped := strings.TrimSuffix(string(w.buf), "\r"), w.dropped
	w.buf, w.dropped = w.buf[:0], false
	l := LogLine{Stream: w.stream, Text: raw}
	// "<RFC3339Nano> <text>" with Timestamps [DK-LOGS].
	if ts, text, ok := strings.Cut(raw, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			l.TS, l.Text = t, text
		}
	}
	if dropped {
		l.Text += truncatedLine
	}
	return w.emit(l)
}

func (w *lineWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	return w.line()
}
