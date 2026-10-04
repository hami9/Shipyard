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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub stands in for GitHub's REST API (P4.4): it issues an
// installation token to a correctly signed App JWT, for installation
// fakeInstallation only, and the git server lets e2e/private be fetched
// only with a contents token it issued [GH-APP-JWT][GH-APP-TOKEN]. Since
// P4.6 it also takes deployments and their statuses, with a deployments
// token [GH-DEPLOY].
type fakeGitHub struct {
	t       *testing.T
	appID   string
	pub     *rsa.PublicKey
	mu      sync.Mutex
	issued  map[string]string // token -> the permission it carries
	fetches int               // authorized fetches of the private repository
	// reported is what reached the Deployments API, in order.
	reported []string
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
	gh = &fakeGitHub{t: t, appID: "e2e-app", pub: &key.PublicKey, issued: map[string]string{}}
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
	if strings.HasPrefix(r.URL.Path, "/repos/") {
		g.deployments(w, r, jwt, refuse)
		return
	}
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
	var perm string
	if json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body) == nil && len(body.Repositories) == 1 &&
		body.Repositories[0] == "private" && len(body.Permissions) == 1 {
		switch {
		case body.Permissions["contents"] == "read":
			perm = "contents"
		case body.Permissions["deployments"] == "write":
			perm = "deployments"
		}
	}
	if perm == "" {
		refuse(http.StatusUnprocessableEntity, "the token must be narrowed to private with contents: read or deployments: write")
		return
	}
	tok := "ghs_e2e" + strings.ToLower(rand.Text())
	g.mu.Lock()
	g.issued[tok] = perm
	g.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"token": tok, "expires_at": time.Now().Add(time.Hour).UTC(),
		"permissions": body.Permissions, "repository_selection": "selected"})
}

// deployments takes a deployment or a status for e2e/private, with a
// deployments token only, and records it as "deployment ref env
// auto_merge required_contexts" or "status id state" (P4.6).
func (g *fakeGitHub) deployments(w http.ResponseWriter, r *http.Request, tok string, refuse func(int, string)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.issued[tok] != "deployments" {
		refuse(http.StatusForbidden, "Resource not accessible by integration")
		return
	}
	var body map[string]any
	json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	rest, ok := strings.CutPrefix(r.URL.Path, "/repos/e2e/private/deployments")
	switch {
	case !ok:
		refuse(http.StatusNotFound, "Not Found")
	case rest == "":
		g.reported = append(g.reported, fmt.Sprintf("deployment %v %v %v %v", body["ref"], body["environment"], body["auto_merge"], body["required_contexts"]))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 500 + len(g.reported)})
	case strings.HasSuffix(rest, "/statuses"):
		g.reported = append(g.reported, fmt.Sprintf("status %s %v", strings.Trim(strings.TrimSuffix(rest, "/statuses"), "/"), body["state"]))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 1})
	default:
		refuse(http.StatusNotFound, "Not Found")
	}
}

func (g *fakeGitHub) reports() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.reported)
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
	if ok && user == "x-access-token" && g.issued[pass] == "contents" {
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
	if ev := h.cli("", "events", op); !strings.Contains(ev, "through GitHub App installation "+fakeInstallation) || strings.Contains(ev, "ghs_") ||
		!strings.Contains(ev, "reporting to GitHub as deployment 501 (environment "+private+")") {
		h.t.Fatalf("events of the private deploy:\n%s", ev)
	}
	// P4.6: the deploy is a GitHub Deployment of its exact commit in the
	// app's environment, in progress and then successful. The two failed
	// deploys ended before a commit was known, so they reported nothing.
	want := []string{"deployment " + h.repo.good + " " + private + " false []", "status 501 in_progress", "status 501 success"}
	if got := h.github.reports(); !slices.Equal(got, want) {
		h.t.Fatalf("reported to GitHub:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if out := h.cli("", "app", "delete", private, "--yes", "--follow"); !strings.Contains(out, "Deleted "+private+".") {
		h.t.Fatalf("delete %s:\n%s", private, out)
	}
}
