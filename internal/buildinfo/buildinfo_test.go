package buildinfo

import (
	"strings"
	"testing"
)

func TestInfoString(t *testing.T) {
	tests := []struct {
		name string
		info Info
		want string
	}{
		{"full commit is shortened", Info{Version: "v1.2.3", Commit: "0123456789abcdef", GoVersion: "go1.27.1"}, "v1.2.3 (commit 0123456789ab, go1.27.1)"},
		{"unknown commit", Info{Version: "dev", GoVersion: "go1.27.1"}, "dev (commit unknown, go1.27.1)"},
		{"dirty tree", Info{Version: "dev", Commit: "abc", Modified: true, GoVersion: "go1.27.1"}, "dev (commit abc-dirty, go1.27.1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.info.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetReportsGoVersion(t *testing.T) {
	info := Get()
	if !strings.HasPrefix(info.GoVersion, "go") {
		t.Errorf("GoVersion = %q, want go-prefixed runtime version", info.GoVersion)
	}
	if info.Version == "" {
		t.Error("Version is empty")
	}
}
