//go:build e2e

package e2e

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub stands in for GitHub's REST API (P4.4): it issues an
// installation token to a correctly signed App JWT, for installation
// fakeInstallation only, and the git server lets e2e/private be fetched
// only with a token it issued [GH-APP-JWT][GH-APP-TOKEN].
type fakeGitHub struct {
	t       *testing.T
	appID   string
	pub     *rsa.PublicKey
	mu      sync.Mutex
	issued  map[string]bool // tokens for the private repository
	fetches int             // authorized fetches of it
}

const fakeInstallation = "77"

// startGitHub writes the App's private key and serves the fake API.
func startGitHub(t *testing.T, dir string) (gh *fakeGitHub, apiURL, keyFile string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyFile = filepath.Join(dir, "github-app.pem")
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyFile, pemKey, 0o600); err != nil {
		t.Fatal(err)
	}
	gh = &fakeGitHub{t: t, appID: "e2e-app", pub: &key.PublicKey, issued: map[string]bool{}}
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	return gh, srv.URL, keyFile
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	refuse := func(status int, msg string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"message": msg})
	}
	if r.Method != http.MethodPost || r.Header.Get("X-GitHub-Api-Version") == "" {
		refuse(http.StatusBadRequest, "bad request")
		return
	}
	jwt, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !g.validJWT(jwt) {
		refuse(http.StatusUnauthorized, "A JSON web token could not be decoded")
		return
	}
	if r.URL.Path != "/app/installations/"+fakeInstallation+"/access_tokens" {
		refuse(http.StatusNotFound, "Not Found")
		return
	}
	var body struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body) != nil || len(body.Repositories) != 1 ||
		body.Repositories[0] != "private" || len(body.Permissions) != 1 || body.Permissions["contents"] != "read" {
		refuse(http.StatusUnprocessableEntity, "the token must be narrowed to private with contents: read")
		return
	}
	tok := "ghs_e2e" + strings.ToLower(rand.Text())
	g.mu.Lock()
	g.issued[tok] = true
	g.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"token": tok, "expires_at": time.Now().Add(time.Hour).UTC(),
		"permissions": body.Permissions, "repository_selection": "selected"})
}

// validJWT checks the RS256 signature, the issuer, and the time window.
func (g *fakeGitHub) validJWT(jwt string) bool {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err != nil || rsa.VerifyPKCS1v15(g.pub, crypto.SHA256, digest[:], sig) != nil {
		return false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		IAT, EXP int64
		ISS      string
	}
	now := time.Now().Unix()
	return json.Unmarshal(raw, &c) == nil && c.ISS == g.appID && c.IAT <= now && c.EXP > now && c.EXP-c.IAT <= 600+60
}

// authorized reports whether a git request for e2e/private carries a token
// this fake issued, as the x-access-token password [GH-APP-TOKEN].
func (g *fakeGitHub) authorized(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	g.mu.Lock()
	defer g.mu.Unlock()
	if ok && user == "x-access-token" && g.issued[pass] {
		g.fetches++
		return true
	}
	return false
}

func (g *fakeGitHub) authorizedFetches() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fetches
}

// checkPrivateRepo (P4.4, Phase 4 exit criterion with a fake GitHub): a
// private repository cannot be fetched without the GitHub App; with an
// installation GitHub refuses it fails; with the right one the worker signs
// a JWT, gets a token for that repository only, and the deploy succeeds.
// The app is deleted again, so the main flow is unaffected.
func (h *harness) checkPrivateRepo() {
	h.t.Helper()
	private := h.slug + "-p"
	h.cli("", "app", "create", private, "--repo", "e2e/private", "--branch", "main", "--port", "8080", "--health-path", "/healthz")
	deploy := func() string {
		out := h.cli("", "deploy", private, "--ref", h.repo.good)
		m := h.opRE.FindStringSubmatch(out)
		if m == nil {
			h.t.Fatalf("deploy %s:\n%s", private, out)
		}
		h.ops = append(h.ops, m[1])
		return m[1]
	}
	h.wantOp(deploy(), "failed", "fetch")
	h.cli("", "app", "update", private, "--github-installation", "78")
	h.wantOp(deploy(), "failed", "GitHub refused an installation token")
	if n := h.github.authorizedFetches(); n != 0 {
		h.t.Fatalf("%d authorized fetches before a token was issued", n)
	}
	h.cli("", "app", "update", private, "--github-installation", fakeInstallation)
	op := deploy()
	h.wantOp(op, "succeeded", "")
	if n := h.github.authorizedFetches(); n == 0 {
		h.t.Fatal("the private repository was fetched without the issued token")
	}
	if ev := h.cli("", "events", op); !strings.Contains(ev, "through GitHub App installation "+fakeInstallation) || strings.Contains(ev, "ghs_") {
		h.t.Fatalf("events of the private deploy:\n%s", ev)
	}
	if out := h.cli("", "app", "delete", private, "--yes", "--follow"); !strings.Contains(out, "Deleted "+private+".") {
		h.t.Fatalf("delete %s:\n%s", private, out)
	}
}
