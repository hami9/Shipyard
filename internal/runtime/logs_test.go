package runtime

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// Docker frames split lines anywhere; lines come out whole, timestamped,
// and bounded.
func TestLineWriter(t *testing.T) {
	var got []string
	w := &lineWriter{stream: "stdout", emit: func(l LogLine) error {
		got = append(got, fmt.Sprintf("%s|%s|%s", l.TS.Format(time.RFC3339Nano), l.Stream, l.Text))
		return nil
	}}
	long := strings.Repeat("x", MaxLogLine+100)
	for _, frame := range []string{
		"2026-09-28T12:00:00.000000001Z hel", "lo\n2026-09-28T12:00:01Z crlf\r\n",
		"2026-09-28T12:00:02Z " + long[:100], long[100:] + "\n",
		"no timestamp here\n2026-09-28T12:00:03Z  two spaces\n", "2026-09-28T12:00:04Z tail",
	} {
		if n, err := w.Write([]byte(frame)); n != len(frame) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	zero := time.Time{}.Format(time.RFC3339Nano)
	want := []string{
		"2026-09-28T12:00:00.000000001Z|stdout|hello",
		"2026-09-28T12:00:01Z|stdout|crlf",
		"2026-09-28T12:00:02Z|stdout|" + long[:MaxLogLine-len("2026-09-28T12:00:02Z")+len(time.RFC3339Nano)] + truncatedLine,
		zero + "|stdout|no timestamp here",
		"2026-09-28T12:00:03Z|stdout| two spaces",
		"2026-09-28T12:00:04Z|stdout|tail",
	}
	if !slices.Equal(got, want) {
		for i := range max(len(got), len(want)) {
			var g, w string
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if g != w {
				t.Errorf("line %d:\n got %.120q (len %d)\nwant %.120q (len %d)", i, g, len(g), w, len(w))
			}
		}
	}
}

// An error from the consumer stops the writer.
func TestLineWriterStops(t *testing.T) {
	stop := errors.New("client gone")
	calls := 0
	w := &lineWriter{stream: "stderr", emit: func(LogLine) error { calls++; return stop }}
	if _, err := w.Write([]byte("a\nb\n")); !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err %v after %d calls", err, calls)
	}
}
