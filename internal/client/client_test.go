package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewURLRules(t *testing.T) {
	ok := []string{"https://shipyard.example.com", "https://1.2.3.4:8443/", "http://127.0.0.1:18080", "http://localhost:8080", "http://[::1]:8080", "unix:///run/shipyard/api.sock", "unix:/run/shipyard/api.sock"}
	for _, u := range ok {
		if _, err := New(u, "shp_x"); err != nil {
			t.Errorf("New(%q) = %v", u, err)
		}
	}
	bad := []string{"http://shipyard.example.com", "http://10.0.0.5:8080", "ftp://x", "shipyard.example.com", "unix://", ""}
	for _, u := range bad {
		if _, err := New(u, "shp_x"); err == nil {
			t.Errorf("New(%q) accepted", u)
		}
	}
	if _, err := New("https://x.example.com", ""); err == nil {
		t.Error("empty token accepted")
	}
}

func TestProblemErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer shp_test" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.EscapedPath() {
		case "/v1/apps":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(422)
			w.Write([]byte(`{"type":"about:blank","title":"Unprocessable Entity","status":422,"detail":"some fields are invalid","errors":[{"field":"slug","detail":"bad"}]}`))
		case "/v1/apps/a%2Fb/env":
			w.WriteHeader(404) // the app segment was escaped, not split
		default:
			w.WriteHeader(502)
			w.Write([]byte("<html>bad gateway</html>"))
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "shp_test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateApp(context.Background(), NewApp{Slug: "Bad"})
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 422 || len(apiErr.Fields) != 1 || !strings.Contains(err.Error(), "slug: bad") {
		t.Fatalf("CreateApp error = %v", err)
	}
	if _, err := c.ListEnv(context.Background(), "a/b"); !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Fatalf("escaped path: %v", err)
	}
	if _, err := c.Whoami(context.Background()); !errors.As(err, &apiErr) || apiErr.Status != 502 || apiErr.Title != "Bad Gateway" {
		t.Fatalf("non-JSON error = %v", err)
	}
}
