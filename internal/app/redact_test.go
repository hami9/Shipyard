package app

import "testing"

func TestRedactor(t *testing.T) {
	r := NewRedactor([]string{"s3cr3t-token", "s3cr3t", "short", "", "pässwört", "s3cr3t"})
	for _, tt := range []struct{ in, want string }{
		{"token=s3cr3t-token done", "token=[REDACTED] done"}, // the longer secret wins
		{"s3cr3ts3cr3t", "[REDACTED][REDACTED]"},
		{"db pässwört!", "db [REDACTED]!"},
		{"short stays: short", "short stays: short"}, // under MinRedactLength
		{"S3CR3T", "S3CR3T"},                         // exact values only
		{"", ""},
	} {
		if got := r.Redact(tt.in); got != tt.want {
			t.Errorf("Redact(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := NewRedactor(nil).Redact("s3cr3t"); got != "s3cr3t" {
		t.Errorf("no secrets: %q", got)
	}
}
