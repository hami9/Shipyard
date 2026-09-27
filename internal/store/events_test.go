package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitize(t *testing.T) {
	tests := map[string]string{
		"plain line":         "plain line",
		"nul\x00byte":        "nul�byte",
		"bad \xff\xfe utf8":  "bad � utf8",
		"ok é 日本":            "ok é 日本",
		"\x1b[31mred\x1b[0m": "\x1b[31mred\x1b[0m", // ANSI colors are valid text
	}
	for in, want := range tests {
		if got := sanitize(in); got != want || !utf8.ValidString(got) {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate(short) = %q", got)
	}
	exact := strings.Repeat("a", 20)
	if got := truncate(exact, 20); got != exact {
		t.Fatal("string at the limit was changed")
	}
	for _, s := range []string{strings.Repeat("a", 100), strings.Repeat("é", 100), strings.Repeat("日本", 60)} {
		got := truncate(s, 40)
		if n := utf8.RuneCountInString(got); n != 40 || !utf8.ValidString(got) || !strings.HasSuffix(got, truncatedMark) {
			t.Fatalf("truncate(%q...) = %q (%d chars, valid %v)", s[:4], got, n, utf8.ValidString(got))
		}
	}
}
