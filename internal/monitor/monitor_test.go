package monitor

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type reports struct{ got []Report }

func (r *reports) ReportChecks(rep Report) { r.got = append(r.got, rep) }

func TestRatios(t *testing.T) {
	if r := (Disk{Used: 80, Avail: 20, Size: 110}).UsedRatio(); r != 0.8 {
		t.Errorf("disk ratio %v, want 0.8 (reserved blocks are not free)", r)
	}
	if r := (Disk{}).UsedRatio(); r != 0 {
		t.Errorf("empty disk ratio %v", r)
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Cert{NotBefore: t0, NotAfter: t0.Add(100 * time.Hour)}
	for at, want := range map[time.Duration]float64{-time.Hour: 0, 0: 0, 80 * time.Hour: 0.8, 200 * time.Hour: 1} {
		if got := c.UsedRatio(t0.Add(at)); got != want {
			t.Errorf("cert ratio at %s = %v, want %v", at, got, want)
		}
	}
	if got := (Cert{Err: errors.New("x")}).UsedRatio(t0); got != 0 {
		t.Errorf("failed cert ratio %v", got)
	}
}

// A check warns when a disk or a certificate passes the threshold, or no
// certificate can be read, once; says so when it is fine again; and
// reports everything each time.
func TestCheckerWarnsOnCrossing(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	used := uint64(50)
	certs := map[string]*x509.Certificate{
		"ok.example":  {NotBefore: t0.Add(-10 * 24 * time.Hour), NotAfter: t0.Add(80 * 24 * time.Hour)},
		"old.example": {NotBefore: t0.Add(-85 * 24 * time.Hour), NotAfter: t0.Add(5 * 24 * time.Hour)},
	}
	hosts := []string{"ok.example", "old.example", "none.example"}
	var logs bytes.Buffer
	rep := &reports{}
	c := &Checker{
		Paths:  []string{"/var/lib/docker"},
		StatFS: func(p string) (Disk, error) { return Disk{Path: p, Used: used, Avail: 100 - used, Size: 100}, nil },
		Hostnames: func(context.Context) ([]string, error) {
			return hosts, nil
		},
		Serve: func(_ context.Context, h string) (*x509.Certificate, error) {
			if c, ok := certs[h]; ok {
				return c, nil
			}
			return nil, errors.New("tls: internal error")
		},
		Report: rep,
		Log:    slog.New(slog.NewTextHandler(&logs, nil)),
		Now:    func() time.Time { return t0 },
	}
	count := func(s string) int { return strings.Count(logs.String(), s) }

	c.Run(t.Context())
	if count("disk is over 80% full") != 0 || count("old.example") != 1 || count("renewal is failing") != 1 ||
		count("no certificate could be read") != 1 {
		t.Fatalf("first check logs:\n%s", logs.String())
	}
	used = 85
	c.Run(t.Context())
	c.Run(t.Context()) // still full: no second warning
	if count("disk is over 80% full") != 1 || count("renewal is failing") != 1 || count("no certificate could be read") != 1 {
		t.Fatalf("logs after the disk filled:\n%s", logs.String())
	}
	used = 10
	hosts = []string{"ok.example"} // the others were removed
	c.Run(t.Context())
	if count("disk is back under the threshold") != 1 || count("fine again") != 0 {
		t.Fatalf("logs after cleanup:\n%s", logs.String())
	}
	hosts = []string{"ok.example", "old.example"} // re-added, still old: warns again
	c.Run(t.Context())
	if count("renewal is failing") != 2 {
		t.Fatalf("a re-added hostname did not warn:\n%s", logs.String())
	}

	if len(rep.got) != 5 {
		t.Fatalf("%d reports, want 5", len(rep.got))
	}
	first := rep.got[0]
	if !first.At.Equal(t0) || len(first.Disks) != 1 || first.Disks[0].Path != "/var/lib/docker" || len(first.Certs) != 3 ||
		first.Certs[2].Hostname != "none.example" || first.Certs[2].Err == nil || !first.Certs[1].NotAfter.Equal(certs["old.example"].NotAfter) {
		t.Fatalf("first report = %+v", first)
	}
}

// Without Caddy, only disks are checked; a disk that cannot be read is
// left out of the report and logged.
func TestCheckerDisksOnly(t *testing.T) {
	var logs bytes.Buffer
	rep := &reports{}
	c := &Checker{Paths: []string{"/a", "/b"}, Report: rep, Log: slog.New(slog.NewTextHandler(&logs, nil)),
		StatFS: func(p string) (Disk, error) {
			if p == "/b" {
				return Disk{}, errors.New("permission denied")
			}
			return Disk{Path: p, Used: 1, Avail: 9}, nil
		}}
	c.Run(t.Context())
	if len(rep.got) != 1 || len(rep.got[0].Disks) != 1 || rep.got[0].Certs != nil || !strings.Contains(logs.String(), "disk check failed") {
		t.Fatalf("report %+v, logs:\n%s", rep.got, logs.String())
	}
}

func TestSorted(t *testing.T) {
	if got := Sorted("/var/lib/docker", "", "/var/lib/shipyard/work", "/var/lib/docker"); strings.Join(got, ",") != "/var/lib/docker,/var/lib/shipyard/work" {
		t.Errorf("Sorted = %q", got)
	}
}

func TestStatFS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	dir := t.TempDir()
	for _, p := range []string{dir, filepath.Join(dir, "not", "yet")} {
		d, err := StatFS(p)
		if err != nil || d.Path != p || d.Size == 0 || d.Used+d.Avail > d.Size || d.UsedRatio() <= 0 || d.UsedRatio() > 1 {
			t.Errorf("StatFS(%s) = %+v, %v", p, d, err)
		}
	}
}

// ServedCert reads the certificate presented for the SNI name, without
// verifying it, and fails on a port that does not speak TLS.
func TestServedCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	want := srv.Certificate()
	got, err := ServedCert(t.Context(), srv.Listener.Addr().String(), "app.example")
	if err != nil || !got.NotAfter.Equal(want.NotAfter) || !got.NotBefore.Equal(want.NotBefore) {
		t.Fatalf("ServedCert = %v, %v; want the test server's", got, err)
	}
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	if _, err := ServedCert(t.Context(), plain.Listener.Addr().String(), "app.example"); err == nil {
		t.Fatal("a plain HTTP port gave a certificate")
	}
}
