package github

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
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
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

var fixedNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// verifyJWT checks an RS256 JWT against pub and returns its claims.
func verifyJWT(t *testing.T, pub *rsa.PublicKey, jwt string) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts", len(parts))
	}
	var header map[string]string
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if json.Unmarshal(h, &header) != nil || header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Fatalf("header = %s", h)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	var claims map[string]any
	c, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if err := json.Unmarshal(c, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

// [GH-APP-JWT]: RS256, iat 60 s in the past, exp at most 10 minutes ahead,
// iss the App's ID.
func TestJWT(t *testing.T) {
	a := &App{ID: "Iv23liExample", Key: key(t), Now: func() time.Time { return fixedNow }}
	jwt, err := a.JWT()
	if err != nil {
		t.Fatal(err)
	}
	c := verifyJWT(t, &a.Key.PublicKey, jwt)
	iat, exp := int64(c["iat"].(float64)), int64(c["exp"].(float64))
	if c["iss"] != "Iv23liExample" || iat != fixedNow.Unix()-60 || exp <= fixedNow.Unix() || exp > fixedNow.Add(10*time.Minute).Unix() {
		t.Fatalf("claims = %v", c)
	}
	if _, err := (&App{Key: key(t)}).JWT(); err == nil {
		t.Fatal("a JWT without an ID")
	}
}

type fakeGitHub struct {
	t      *testing.T
	pub    *rsa.PublicKey
	status int
	reply  string
	got    struct {
		path, accept, version string
		body                  map[string]any
	}
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Method != http.MethodPost || !ok {
		http.Error(w, `{"message":"Requires authentication"}`, http.StatusUnauthorized)
		return
	}
	verifyJWT(f.t, f.pub, auth)
	f.got.path, f.got.accept, f.got.version = r.URL.Path, r.Header.Get("Accept"), r.Header.Get("X-GitHub-Api-Version")
	b, _ := io.ReadAll(r.Body)
	json.Unmarshal(b, &f.got.body)
	w.WriteHeader(f.status)
	io.WriteString(w, f.reply)
}

func newFake(t *testing.T, status int, reply string) (*fakeGitHub, *App) {
	f := &fakeGitHub{t: t, pub: &key(t).PublicKey, status: status, reply: reply}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &App{ID: "123", Key: key(t), APIURL: srv.URL + "/", HTTP: srv.Client()}
}

// [GH-APP-TOKEN]: one POST per token, narrowed to the repository's name
// and to contents: read.
func TestInstallationToken(t *testing.T) {
	f, a := newFake(t, http.StatusCreated, `{"token":"ghs_secretvalue","expires_at":"2026-10-04T13:00:00Z",
		"permissions":{"contents":"read"},"repository_selection":"selected"}`)
	tok, err := a.InstallationToken(t.Context(), 42, "octo/private")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != "ghs_secretvalue" || !tok.ExpiresAt.Equal(time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("token = %+v %v", tok.Value, tok.ExpiresAt)
	}
	if f.got.path != "/app/installations/42/access_tokens" || f.got.accept != "application/vnd.github+json" || f.got.version != apiVersion {
		t.Fatalf("request = %+v", f.got)
	}
	if b, _ := json.Marshal(f.got.body); string(b) != `{"permissions":{"contents":"read"},"repositories":["private"]}` {
		t.Fatalf("body = %s", b)
	}
	// The token does not print.
	if s := fmt.Sprintf("%v %+v %#v %s", tok, tok, tok, tok); strings.Contains(s, "ghs_") {
		t.Fatalf("token printed: %s", s)
	}
}

func TestInstallationTokenFails(t *testing.T) {
	for name, tc := range map[string]struct {
		status       int
		reply, repo  string
		installation int64
		want         string
	}{
		"not installed": {http.StatusNotFound, `{"message":"Not Found"}`, "octo/app", 42, `404 Not Found "Not Found"`},
		"no access":     {http.StatusUnprocessableEntity, `{"message":"There is at least one repository that does not exist or is not accessible"}`, "octo/app", 42, "422"},
		"no token":      {http.StatusCreated, `{"expires_at":"2026-10-04T13:00:00Z"}`, "octo/app", 42, "has no token"},
		"not json":      {http.StatusCreated, `<html>`, "octo/app", 42, "has no token"},
		"bad repo":      {http.StatusCreated, `{}`, "octo", 42, "not owner/name"},
		"nested repo":   {http.StatusCreated, `{}`, "octo/a/b", 42, "not owner/name"},
		"no install":    {http.StatusCreated, `{}`, "octo/app", 0, "installation id"},
	} {
		t.Run(name, func(t *testing.T) {
			_, a := newFake(t, tc.status, tc.reply)
			_, err := a.InstallationToken(t.Context(), tc.installation, tc.repo)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func writeKey(t *testing.T, dir, name string, block *pem.Block, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pem.EncodeToMemory(block), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
	return p
}

func TestLoadKey(t *testing.T) {
	dir := t.TempDir()
	k := key(t)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(k)
	for name, path := range map[string]string{
		"pkcs1": writeKey(t, dir, "pkcs1.pem", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}, 0o400),
		"pkcs8": writeKey(t, dir, "pkcs8.pem", &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}, 0o600),
	} {
		got, err := LoadKey(path)
		if err != nil || !got.Equal(k) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecDER, _ := x509.MarshalPKCS8PrivateKey(ec)
	for name, path := range map[string]string{
		"world-readable": writeKey(t, dir, "open.pem", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}, 0o644),
		// P5.8: the API shares the worker's group, so group read is refused.
		"group-readable": writeKey(t, dir, "group.pem", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}, 0o640),
		"too small":      writeKey(t, dir, "small.pem", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(small)}, 0o600),
		"not rsa":        writeKey(t, dir, "ec.pem", &pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}, 0o600),
		"public key":     writeKey(t, dir, "pub.pem", &pem.Block{Type: "PUBLIC KEY", Bytes: []byte("x")}, 0o600),
		"garbage":        writeKey(t, dir, "bad.pem", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("not a key")}, 0o600),
		"missing":        filepath.Join(dir, "none.pem"),
		"directory":      dir,
	} {
		if _, err := LoadKey(path); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "BEGIN") {
			t.Errorf("%s: the error shows the key: %v", name, err)
		}
	}
}
