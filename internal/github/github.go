// Package github authenticates Shipyard as a GitHub App. It signs the App's
// JWT and exchanges it for an installation token that can only read one
// repository and expires within the hour [GH-APP-JWT][GH-APP-TOKEN]. Only
// the worker uses it, once per fetch; tokens are never stored or logged.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultAPIURL is GitHub's REST API.
	DefaultAPIURL = "https://api.github.com"
	// apiVersion is the REST API version requested [GH-APP-TOKEN].
	apiVersion = "2026-03-10"
	// The JWT is backdated 60 s for clock drift and lives 9 minutes, under
	// GitHub's 10-minute limit [GH-APP-JWT].
	jwtBackdate = 60 * time.Second
	jwtLifetime = 9 * time.Minute
	minKeyBits  = 2048
	maxResponse = 1 << 20
)

// App is a GitHub App's identity.
type App struct {
	// ID is the App's client ID (GitHub's recommendation) or its numeric
	// app ID: the JWT's issuer.
	ID     string
	Key    *rsa.PrivateKey
	APIURL string       // DefaultAPIURL when empty; tests use a loopback server
	HTTP   *http.Client // http.DefaultClient when nil
	Now    func() time.Time
}

// Token is an installation access token. It formats as [token], so it does
// not end up in a log by accident.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

func (Token) String() string   { return "[token]" }
func (Token) GoString() string { return "[token]" }

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// JWT returns a token that authenticates as the App itself, for the
// installation token request only.
func (a *App) JWT() (string, error) {
	if a.Key == nil || a.ID == "" {
		return "", errors.New("the GitHub App has no ID or key")
	}
	now := a.now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(struct {
		IAT int64  `json:"iat"`
		EXP int64  `json:"exp"`
		ISS string `json:"iss"`
	}{now.Add(-jwtBackdate).Unix(), now.Add(jwtLifetime).Unix(), a.ID})
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.Key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign the GitHub App JWT: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// InstallationToken asks GitHub for a token of the installation that can
// read the contents of repo (owner/name) and nothing else [GH-APP-TOKEN].
func (a *App) InstallationToken(ctx context.Context, installationID int64, repo string) (Token, error) {
	return a.TokenWith(ctx, installationID, repo, map[string]string{"contents": "read"})
}

// TokenWith asks for a token of the installation limited to repo and to
// permissions, e.g. {"deployments": "write"} [GH-APP-TOKEN].
func (a *App) TokenWith(ctx context.Context, installationID int64, repo string, permissions map[string]string) (Token, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Token{}, fmt.Errorf("repository %q is not owner/name", repo)
	}
	if installationID <= 0 {
		return Token{}, errors.New("no GitHub App installation id")
	}
	jwt, err := a.JWT()
	if err != nil {
		return Token{}, err
	}
	body, _ := json.Marshal(map[string]any{
		"repositories": []string{name}, // names within the installation's account
		"permissions":  permissions,
	})
	raw, err := a.post(ctx, jwt, "/app/installations/"+strconv.FormatInt(installationID, 10)+"/access_tokens", body)
	if se := (*statusError)(nil); errors.As(err, &se) {
		return Token{}, fmt.Errorf("GitHub refused an installation token for %s (installation %d): %w", repo, installationID, err)
	}
	if err != nil {
		return Token{}, fmt.Errorf("request an installation token: %w", err)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return Token{}, errors.New("GitHub's installation token response has no token")
	}
	return Token{Value: out.Token, ExpiresAt: out.ExpiresAt}, nil
}

// statusError is a GitHub answer other than 201 Created.
type statusError struct {
	status, message string
}

func (e *statusError) Error() string { return fmt.Sprintf("%s %.200q", e.status, e.message) }

// post sends a JSON body with a bearer credential (the App's JWT or an
// installation token) and returns the body of a 201 answer.
func (a *App) post(ctx context.Context, bearer, path string, body []byte) ([]byte, error) {
	base := strings.TrimSuffix(a.APIURL, "/")
	if base == "" {
		base = DefaultAPIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "shipyard")
	hc := a.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		var e struct {
			Message string `json:"message"`
		}
		json.Unmarshal(raw, &e)
		return nil, &statusError{status: resp.Status, message: e.Message}
	}
	return raw, nil
}

// LoadKey reads the App's private key: a PEM RSA key (PKCS#1, as GitHub
// generates it, or PKCS#8) of at least 2048 bits, in a regular file only
// its owner, the worker's user, can read. The API shares the worker's
// group, so a group bit is refused, as for HPKE private keys (ADR-0012;
// P5.8). It stays out of the database. Errors never contain the key.
func LoadKey(path string) (*rsa.PrivateKey, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("GitHub App key %s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("GitHub App key %s is accessible to others than its owner (mode %v): chown it to the worker's user and chmod 0600",
			path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("GitHub App key %s is not PEM", path)
	}
	var key any
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("GitHub App key %s is a %q block, not a private key", path, block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("GitHub App key %s cannot be parsed", path)
	}
	rk, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("GitHub App key %s is not an RSA key (RS256 needs one)", path)
	}
	if rk.N.BitLen() < minKeyBits {
		return nil, fmt.Errorf("GitHub App key %s has %d bits, fewer than %d", path, rk.N.BitLen(), minKeyBits)
	}
	return rk, nil
}
