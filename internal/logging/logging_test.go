package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestContextFieldsAreLogged(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo, FormatJSON).With("component", "test")

	ctx := With(context.Background(), RequestID, "req-1")
	ctx = With(ctx, OperationID, "op-7")
	log.InfoContext(ctx, "hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}
	for k, want := range map[string]string{"msg": "hello", "request_id": "req-1", "operation_id": "op-7", "component": "test"} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %q", k, rec[k], want)
		}
	}
	if _, ok := rec["app"]; ok {
		t.Error("absent field app was logged")
	}
}

func TestLevelFilters(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelWarn, FormatText)
	log.Info("hidden")
	log.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"json", FormatJSON, false},
		{"TEXT", FormatText, false},
		{"yaml", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := ParseFormat(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseFormat(%q) = %q, %v; want %q, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}
