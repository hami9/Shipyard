// Package webhook verifies and reads GitHub webhook deliveries. It does no
// I/O besides LoadSecret: the API's receiver reads the body, and this
// package decides whether it is genuine and what it asks for (CLAUDE.md
// invariant 9).
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	// MaxPayload is GitHub's cap on a delivery; larger events are not sent
	// at all [GH-EVENTS].
	MaxPayload = 25 << 20
	// MinSecretLen is the shortest webhook secret LoadSecret accepts. GitHub
	// asks for a random string with high entropy [GH-BP].
	MinSecretLen = 16

	// The delivery headers [GH-EVENTS].
	HeaderSignature = "X-Hub-Signature-256"
	HeaderEvent     = "X-GitHub-Event"
	HeaderDelivery  = "X-GitHub-Delivery"

	signaturePrefix = "sha256="
	branchPrefix    = "refs/heads/"
	maxRefLen       = 255 // webhook_deliveries.ref
)

var (
	// ErrUnsigned is a request without X-Hub-Signature-256. The legacy
	// SHA-1 header is never consulted [GH-VALIDATE].
	ErrUnsigned = errors.New("the request has no " + HeaderSignature + " header")
	// ErrSignature is a signature that is malformed or does not match.
	ErrSignature = errors.New("the signature does not match the payload")
	// ErrNoSecret refuses to verify against an empty secret, whose HMAC
	// anyone can compute.
	ErrNoSecret = errors.New("no webhook secret is configured")

	// deliveryRE mirrors the webhook_deliveries.delivery_id CHECK.
	deliveryRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	// shaRE is a full SHA-1 or SHA-256 object name, as GitHub sends it
	// (mirrors webhook_deliveries.after_sha).
	shaRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// Sign returns the X-Hub-Signature-256 value GitHub sends for body: sha256=
// and the hex HMAC-SHA256 of the raw payload under secret [GH-VALIDATE].
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks header against the raw body before anything parses it. The
// digests are compared in constant time [GH-VALIDATE]; the error says only
// that the check failed, never what was expected.
func Verify(secret, body []byte, header string) error {
	if len(secret) == 0 {
		return ErrNoSecret
	}
	if header == "" {
		return ErrUnsigned
	}
	digest, ok := strings.CutPrefix(header, signaturePrefix)
	if !ok {
		return ErrSignature
	}
	got, err := hex.DecodeString(digest)
	if err != nil || len(got) != sha256.Size {
		return ErrSignature
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return ErrSignature
	}
	return nil
}

// ValidDelivery reports whether id can be an X-GitHub-Delivery GUID as the
// database stores it.
func ValidDelivery(id string) bool { return deliveryRE.MatchString(id) }

// Push is what a push event asks Shipyard to deploy: a commit at the head
// of a branch of a repository.
type Push struct {
	RepositoryID int64  // GitHub's numeric id; it survives a rename
	Repository   string // owner/name, for logs only
	Ref          string // refs/heads/<Branch>
	Branch       string
	After        string // the branch head after the push
}

// pushPayload is the part of a push event Shipyard reads [GH-EVENTS].
type pushPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository *struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// ParsePush reads a verified push payload. A push that cannot lead to a
// deploy (a deleted ref, a tag or other non-branch ref) comes back with
// the reason it is ignored; a payload that is not a well-formed push is an
// error.
func ParsePush(body []byte) (Push, string, error) {
	var p pushPayload
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&p); err != nil {
		return Push{}, "", fmt.Errorf("the payload is not JSON: %w", err)
	}
	if dec.More() {
		return Push{}, "", errors.New("the payload has data after the JSON object")
	}
	if p.Repository == nil || p.Repository.ID <= 0 {
		return Push{}, "", errors.New("the payload has no repository id")
	}
	if p.Deleted {
		return Push{}, "the push deleted the ref", nil
	}
	branch, ok := strings.CutPrefix(p.Ref, branchPrefix)
	if !ok || branch == "" {
		return Push{}, "the ref is not a branch", nil
	}
	if len(p.Ref) > maxRefLen || strings.ContainsFunc(p.Ref, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return Push{}, "", errors.New("the ref is not a valid branch name")
	}
	if !shaRE.MatchString(p.After) || strings.Trim(p.After, "0") == "" {
		return Push{}, "", errors.New("after is not a commit SHA")
	}
	return Push{RepositoryID: p.Repository.ID, Repository: p.Repository.FullName, Ref: p.Ref, Branch: branch, After: p.After}, "", nil
}

// LoadSecret reads the webhook secret from path: one line, at least
// MinSecretLen bytes, in a regular file other users cannot read (like the
// KEK files, ADR-0005). A trailing newline is dropped. Errors never contain
// the file's content.
func LoadSecret(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("webhook secret %s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o007 != 0 {
		return nil, fmt.Errorf("webhook secret %s is accessible to other users (mode %v); chmod 0640", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimRight(b, "\r\n")
	if len(b) < MinSecretLen {
		return nil, fmt.Errorf("webhook secret %s is shorter than %d bytes", path, MinSecretLen)
	}
	return b, nil
}
