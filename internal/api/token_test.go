package api

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestNewToken(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		tok, prefix, hash := NewToken()
		if len(tok) != tokenLen || !wellFormedToken(tok) {
			t.Fatalf("NewToken() = %q, not well formed", tok)
		}
		if !strings.HasPrefix(tok, prefix) || len(prefix) != len("shp_")+8 {
			t.Fatalf("prefix %q does not start token %q", prefix, tok)
		}
		if sum := sha256.Sum256([]byte(tok)); !bytes.Equal(hash, sum[:]) || !bytes.Equal(HashToken(tok), hash) {
			t.Fatal("hash is not SHA-256 of the plaintext")
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
}

func TestWellFormedToken(t *testing.T) {
	good, _, _ := NewToken()
	bad := []string{
		"",
		"shp_",
		good[:len(good)-1],
		good + "A",
		"ghp_" + good[4:],
		"SHP_" + good[4:],
		good[:10] + "=" + good[11:],
		good[:10] + "+" + good[11:],
		good[:10] + " " + good[11:],
	}
	if !wellFormedToken(good) {
		t.Fatalf("%q rejected", good)
	}
	for _, b := range bad {
		if wellFormedToken(b) {
			t.Errorf("wellFormedToken(%q) = true", b)
		}
	}
}

func TestHasScope(t *testing.T) {
	tests := []struct {
		granted []string
		need    string
		want    bool
	}{
		{[]string{ScopeRead}, ScopeRead, true},
		{[]string{ScopeRead}, ScopeDeploy, false},
		{[]string{ScopeRead}, ScopeAdmin, false},
		{[]string{ScopeDeploy}, ScopeRead, true},
		{[]string{ScopeDeploy}, ScopeDeploy, true},
		{[]string{ScopeDeploy}, ScopeAdmin, false},
		{[]string{ScopeAdmin}, ScopeDeploy, true},
		{[]string{"bogus", ScopeRead}, ScopeRead, true},
		{[]string{"bogus"}, ScopeRead, false},
		{nil, ScopeRead, false},
		{[]string{ScopeAdmin}, "bogus", false}, // unknown requirement never passes
	}
	for _, tt := range tests {
		if got := hasScope(tt.granted, tt.need); got != tt.want {
			t.Errorf("hasScope(%v, %q) = %v, want %v", tt.granted, tt.need, got, tt.want)
		}
	}
}
