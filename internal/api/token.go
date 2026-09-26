package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// API tokens are "shp_" plus 32 random bytes in unpadded base64url
// (ARCHITECTURE §4). Only the SHA-256 hash is stored; the plaintext is shown
// once (ADR-0007).
const (
	TokenPrefix     = "shp_"
	tokenRandomLen  = 32
	tokenLen        = len(TokenPrefix) + 43 // base64url of 32 bytes, unpadded
	tokenDisplayLen = len(TokenPrefix) + 8
)

// Scopes, from least to most privileged. Each includes those before it.
const (
	ScopeRead   = "read"   // list and inspect
	ScopeDeploy = "deploy" // plus trigger deploys and rollbacks, e.g. from CI
	ScopeAdmin  = "admin"  // everything
)

var scopeRank = map[string]int{ScopeRead: 1, ScopeDeploy: 2, ScopeAdmin: 3}

// ValidScope reports whether s is a known scope.
func ValidScope(s string) bool { return scopeRank[s] > 0 }

// hasScope reports whether any granted scope includes need.
func hasScope(granted []string, need string) bool {
	for _, g := range granted {
		if scopeRank[g] >= scopeRank[need] && scopeRank[need] > 0 {
			return true
		}
	}
	return false
}

// NewToken returns a fresh token, its display prefix, and its hash.
func NewToken() (plaintext, prefix string, hash []byte) {
	b := make([]byte, tokenRandomLen)
	rand.Read(b) // never returns an error [GO-RAND]
	plaintext = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, plaintext[:tokenDisplayLen], HashToken(plaintext)
}

// HashToken returns the SHA-256 digest under which a token is stored.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// wellFormedToken rejects anything that cannot be a Shipyard token before
// it reaches the database.
func wellFormedToken(t string) bool {
	if len(t) != tokenLen || !strings.HasPrefix(t, TokenPrefix) {
		return false
	}
	for _, c := range t[len(TokenPrefix):] {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
