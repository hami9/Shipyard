//go:build docker

package runtime

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// P2.7b: a container's output streams line by line, split by stream and
// timestamped [DK-LOGS]; follow ends when the container stops.
func TestStreamLogs(t *testing.T) {
	r := newRuntime(t)
	ctx := t.Context()
	id := startHardened(t, r, spec(testApp(t), probeImage))
	st, err := r.Inspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute)
	fetch(t, st.IP, "/say?text=hello+out")
	fetch(t, st.IP, "/say?stream=stderr&text=oops+err")

	var got []string
	if err := r.StreamLogs(ctx, id, 10, false, func(l LogLine) error {
		if l.TS.Before(start) || l.TS.After(time.Now().Add(time.Minute)) {
			t.Errorf("line %q has timestamp %s", l.Text, l.TS)
		}
		got = append(got, l.Stream+" "+l.Text)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The probe's own startup line comes from the log package on stderr.
	if len(got) != 3 || !strings.HasPrefix(got[0], "stderr ") || !strings.HasSuffix(got[0], "probe listening on :8080") ||
		got[1] != "stdout hello out" || got[2] != "stderr oops err" {
		t.Fatalf("lines = %q", got)
	}
	// A tail of 1 is the last line only.
	got = nil
	r.StreamLogs(ctx, id, 1, false, func(l LogLine) error { got = append(got, l.Text); return nil })
	if !slices.Equal(got, []string{"oops err"}) {
		t.Fatalf("tail 1 = %q", got)
	}

	// Follow: a new line arrives live; stopping the container ends it.
	lines, done := make(chan string, 10), make(chan error, 1)
	go func() {
		done <- r.StreamLogs(ctx, id, 0, true, func(l LogLine) error { lines <- l.Text; return nil })
	}()
	// A line printed before the stream attached is not in a tail of 0, so
	// print until one arrives.
	for deadline := time.Now().Add(10 * time.Second); ; {
		fetch(t, st.IP, "/say?text=live")
		select {
		case l := <-lines:
			if l != "live" {
				t.Fatalf("followed %q", l)
			}
		case <-time.After(200 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("no live line")
			}
			continue
		}
		break
	}
	if err := r.Stop(ctx, id, time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("follow ended with %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("follow did not end when the container stopped")
	}

	// An error from the consumer stops the stream and is returned.
	stop := errors.New("reader gone")
	if err := r.StreamLogs(ctx, id, 10, false, func(LogLine) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("consumer error: %v", err)
	}
}
