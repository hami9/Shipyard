// Package monitor checks the host's disks and the certificates Caddy
// serves, and warns when one passes a threshold (ADR-0014). It runs in the
// worker; the metrics and the shipped Prometheus rules alert on the same
// numbers.
package monitor

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// DefaultThreshold is the share of a disk, or of a certificate's lifetime,
// past which a check warns (ROADMAP P5.6). Caddy renews when a third of a
// certificate's lifetime remains [CM-RENEW], so 80% used means renewal has
// been failing for a while.
const DefaultThreshold = 0.8

// Disk is one filesystem's usage, as df reports it.
type Disk struct {
	Path        string // a path on the filesystem
	Size, Avail uint64 // bytes; Avail is what an unprivileged user may still write
	Used        uint64 // bytes in use
}

// UsedRatio is used / (used + avail), df's Use%: the reserved blocks only
// root may write do not count as free.
func (d Disk) UsedRatio() float64 {
	if d.Used+d.Avail == 0 {
		return 0
	}
	return float64(d.Used) / float64(d.Used+d.Avail)
}

// Cert is the certificate served for one hostname, or why none was read.
type Cert struct {
	Hostname            string
	NotBefore, NotAfter time.Time
	Err                 error
}

// UsedRatio is how much of the certificate's lifetime has passed at now.
func (c Cert) UsedRatio(now time.Time) float64 {
	life := c.NotAfter.Sub(c.NotBefore)
	if c.Err != nil || life <= 0 {
		return 0
	}
	return min(max(float64(now.Sub(c.NotBefore))/float64(life), 0), 1)
}

// Report is one check's results.
type Report struct {
	At    time.Time
	Disks []Disk
	Certs []Cert
}

// Reporter receives every report (the worker's metrics).
type Reporter interface {
	ReportChecks(Report)
}

// Checker runs the checks. StatFS and Paths cover the disks; Hostnames and
// Serve cover the certificates, and are nil when Caddy is disabled.
type Checker struct {
	Paths     []string
	StatFS    func(path string) (Disk, error)
	Hostnames func(ctx context.Context) ([]string, error)
	// Serve returns the leaf certificate Caddy presents for a hostname.
	Serve     func(ctx context.Context, hostname string) (*x509.Certificate, error)
	Threshold float64 // 0 means DefaultThreshold
	Report    Reporter
	Log       *slog.Logger
	Now       func() time.Time

	// over holds what warned at the last check, by "disk:"/"cert:" key, so a
	// condition is logged when it starts and when it ends, not every check.
	over map[string]bool
}

// Run checks once. A failing check is reported and logged, never fatal.
func (c *Checker) Run(ctx context.Context) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	threshold := c.Threshold
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	if c.over == nil {
		c.over = map[string]bool{}
	}
	r := Report{At: now()}
	seen := map[string]bool{}
	for _, p := range c.Paths {
		d, err := c.StatFS(p)
		if err != nil {
			c.Log.Warn("disk check failed", slog.String("path", p), slog.Any("err", err))
			continue
		}
		r.Disks = append(r.Disks, d)
		key := "disk:" + p
		seen[key] = true
		c.flip(key, d.UsedRatio() >= threshold,
			func() {
				c.Log.Warn(fmt.Sprintf("disk is over %.0f%% full", threshold*100), slog.String("path", p),
					slog.String("used", pct(d.UsedRatio())), slog.Uint64("avail_bytes", d.Avail))
			},
			func() {
				c.Log.Info("disk is back under the threshold", slog.String("path", p), slog.String("used", pct(d.UsedRatio())))
			})
	}
	if c.Hostnames != nil {
		hosts, err := c.Hostnames(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.Log.Warn("certificate check: list hostnames failed", slog.Any("err", err))
		}
		for _, h := range hosts {
			cert := c.cert(ctx, h)
			if ctx.Err() != nil {
				return
			}
			r.Certs = append(r.Certs, cert)
			key := "cert:" + h
			seen[key] = true
			used := cert.UsedRatio(r.At)
			c.flip(key, cert.Err != nil || used >= threshold,
				func() {
					if cert.Err != nil {
						c.Log.Warn("no certificate could be read", slog.String("hostname", h), slog.Any("err", cert.Err))
						return
					}
					c.Log.Warn(fmt.Sprintf("certificate is over %.0f%% through its lifetime: renewal is failing", threshold*100),
						slog.String("hostname", h), slog.String("used", pct(used)), slog.Time("not_after", cert.NotAfter))
				},
				func() {
					c.Log.Info("certificate is fine again", slog.String("hostname", h), slog.Time("not_after", cert.NotAfter))
				})
		}
	}
	for key := range c.over {
		if !seen[key] {
			delete(c.over, key) // a removed hostname or path
		}
	}
	if c.Report != nil {
		c.Report.ReportChecks(r)
	}
}

func (c *Checker) cert(ctx context.Context, hostname string) Cert {
	leaf, err := c.Serve(ctx, hostname)
	if err != nil {
		return Cert{Hostname: hostname, Err: err}
	}
	return Cert{Hostname: hostname, NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter}
}

// flip logs when a condition starts (up) or ends (down).
func (c *Checker) flip(key string, bad bool, up, down func()) {
	switch was := c.over[key]; {
	case bad && !was:
		up()
	case !bad && was:
		down()
	}
	c.over[key] = bad
}

func pct(r float64) string { return fmt.Sprintf("%.1f%%", r*100) }

// Sorted returns paths without duplicates, in order, for a stable report.
func Sorted(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
