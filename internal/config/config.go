// Package config loads process configuration from environment variables.
//
// Environment-only configuration keeps the standard library sufficient: in
// production systemd supplies the variables through EnvironmentFile=
// (deploy/shipyard.env.example). Values are validated once at startup and the
// resulting structs are passed down explicitly; nothing reads the environment
// later.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hami9/shipyard/internal/logging"
)

// Environment variable names.
const (
	EnvDatabaseURL        = "SHIPYARD_DATABASE_URL"
	EnvLogLevel           = "SHIPYARD_LOG_LEVEL"
	EnvLogFormat          = "SHIPYARD_LOG_FORMAT"
	EnvShutdownTimeout    = "SHIPYARD_SHUTDOWN_TIMEOUT"
	EnvAPIListen          = "SHIPYARD_API_LISTEN"
	EnvAPIHostname        = "SHIPYARD_API_HOSTNAME"
	EnvWorkerID           = "SHIPYARD_WORKER_ID"
	EnvWorkerPollInterval = "SHIPYARD_WORKER_POLL_INTERVAL"
	EnvKEKDir             = "SHIPYARD_KEK_DIR"
	EnvKEKActive          = "SHIPYARD_KEK_ACTIVE"
	EnvWorkDir            = "SHIPYARD_WORK_DIR"
	EnvSourceBaseURL      = "SHIPYARD_SOURCE_BASE_URL"
	EnvBuilderName        = "SHIPYARD_BUILDER"
	EnvBuilderMemory      = "SHIPYARD_BUILDER_MEMORY"
	EnvBuilderCPUs        = "SHIPYARD_BUILDER_CPUS"
	EnvCaddy              = "SHIPYARD_CADDY"
	EnvCaddyName          = "SHIPYARD_CADDY_NAME"
	EnvCaddyImage         = "SHIPYARD_CADDY_IMAGE"
	EnvCaddyAdminDir      = "SHIPYARD_CADDY_ADMIN_DIR"
	EnvCaddyBind          = "SHIPYARD_CADDY_BIND"
	EnvCaddyHTTPPort      = "SHIPYARD_CADDY_HTTP_PORT"
	EnvCaddyHTTPSPort     = "SHIPYARD_CADDY_HTTPS_PORT"
	EnvCaddyCA            = "SHIPYARD_CADDY_CA"
	EnvACMEEmail          = "SHIPYARD_ACME_EMAIL"
	EnvReconcileInterval  = "SHIPYARD_RECONCILE_INTERVAL"
	EnvObservationWindow  = "SHIPYARD_OBSERVATION_WINDOW"
	EnvWorkerSocket       = "SHIPYARD_WORKER_SOCKET"
	EnvRetainImages       = "SHIPYARD_RETAIN_IMAGES"
	EnvRetainOperations   = "SHIPYARD_RETAIN_OPERATIONS"
	EnvOperationLogMax    = "SHIPYARD_OPERATION_LOG_MAX"
	EnvBuildCacheMax      = "SHIPYARD_BUILD_CACHE_MAX"
	EnvRetentionInterval  = "SHIPYARD_RETENTION_INTERVAL"
	EnvBackupDir          = "SHIPYARD_BACKUP_DIR"
	EnvBackupKEKDir       = "SHIPYARD_BACKUP_KEK_DIR"
	EnvBackupHook         = "SHIPYARD_BACKUP_HOOK"
	EnvBackupKEKHook      = "SHIPYARD_BACKUP_KEK_HOOK"
	EnvBackupPGDump       = "SHIPYARD_BACKUP_PG_DUMP"
	EnvBackupKeepDaily    = "SHIPYARD_BACKUP_KEEP_DAILY"
	EnvBackupKeepWeekly   = "SHIPYARD_BACKUP_KEEP_WEEKLY"
	EnvPublicIPs          = "SHIPYARD_PUBLIC_IPS"
	EnvDomainSuffixes     = "SHIPYARD_DOMAIN_SUFFIXES"
	EnvDNSPreflight       = "SHIPYARD_DNS_PREFLIGHT"
)

// Defaults.
const (
	DefaultAPIListen       = "127.0.0.1:8080"
	DefaultShutdownTimeout = 15 * time.Second
	DefaultPollInterval    = 2 * time.Second
	// DefaultReconcileInterval is ARCHITECTURE §5's reconciler period.
	DefaultReconcileInterval = time.Minute
	// DefaultObservationWindow is how long a superseded container keeps
	// running after a switch (ARCHITECTURE §5 step 8).
	DefaultObservationWindow = 5 * time.Minute
	DefaultKEKDir            = "/etc/shipyard/kek"
	DefaultWorkDir           = "/var/lib/shipyard/work"
	DefaultSourceBaseURL     = "https://github.com"
	DefaultBuilderName       = "shipyard"
	DefaultBuilderMemory     = "2g"
	DefaultBuilderCPUs       = 2.0
	DefaultCaddyName         = "shipyard-caddy"
	DefaultCaddyAdminDir     = "/run/shipyard/caddy"
	// DefaultWorkerSocket is in the worker's systemd RuntimeDirectory (ADR-0008).
	DefaultWorkerSocket = "/run/shipyard-worker/logs.sock"
	// DefaultRetainImages is how many earlier releases per app keep their
	// image for rollback, besides the active one (ADR-0006).
	DefaultRetainImages = 5
	maxRetainImages     = 1000
	// ADR-0006: operation events of the last 20 operations per app, 5 MB
	// each; the BuildKit cache pruned daily down to 10 GB.
	DefaultRetainOperations  = 20
	maxRetainOperations      = 10000
	DefaultOperationLogMax   = 5 << 20
	DefaultBuildCacheMax     = 10 << 30
	DefaultRetentionInterval = 24 * time.Hour
	maxSize                  = 1 << 50
	// ADR-0006: backups go to two separate targets, A (the database and
	// Caddy's data) and B (the KEKs); A keeps 14 dailies and 8 weeklies.
	DefaultBackupDir        = "/var/backups/shipyard"
	DefaultBackupKEKDir     = "/var/backups/shipyard-kek"
	DefaultBackupPGDump     = "pg_dump"
	DefaultBackupKeepDaily  = 14
	DefaultBackupKeepWeekly = 8

	minPollInterval = 100 * time.Millisecond
)

var (
	// kekIDRE mirrors secret_values.kek_id.
	kekIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// sizeRE is a byte count with an optional binary unit and b (reader.size).
	sizeRE = regexp.MustCompile(`^([1-9][0-9]{0,15})([kmgt]?)b?$`)
	// memoryRE is a buildx driver-opt memory value, e.g. 512m or 2g [DK-BX-CONTAINER].
	memoryRE = regexp.MustCompile(`^[1-9][0-9]*[bkmg]?$`)
	// builderRE is a safe buildx builder name; it becomes a container name.
	builderRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	// suffixRE is a domain suffix: DNS labels with at least one dot, the last
	// one alphabetic (mirrors the routes.hostname CHECK).
	suffixRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// LookupFunc has the signature of os.LookupEnv; tests pass a map lookup.
type LookupFunc func(key string) (string, bool)

// Log configures the process logger.
type Log struct {
	Level  slog.Level
	Format logging.Format
}

// Common holds settings shared by the API and the worker.
type Common struct {
	Log Log
	// DatabaseURL contains credentials. Never log it.
	DatabaseURL     string
	ShutdownTimeout time.Duration
	// KEKDir holds <kek_id>.key files; KEKActive names the one that seals new
	// values (ADR-0005). Commands that never touch secrets, such as migrate,
	// do not need a KEK, so KEKActive is required only where it is used.
	KEKDir    string
	KEKActive string
	// WorkerSocket is where the worker serves app logs and the API reads
	// them (ADR-0008).
	WorkerSocket string
	// Listen is the API's address: loopback host:port or
	// unix:/absolute/path.sock, never a public one (P2.8). The worker reads it
	// too, to publish the API's socket through Caddy.
	Listen string
	// APIHostname, when set, is the public name Caddy serves the API on over
	// HTTPS; it needs a Unix socket Listen. The API refuses it as an app
	// domain.
	APIHostname string
}

// APISocket is the path of the API's Unix socket, or "" for TCP.
func (c Common) APISocket() string {
	path, _ := strings.CutPrefix(c.Listen, "unix:")
	if path == c.Listen {
		return ""
	}
	return path
}

// API configures shipyard-api.
type API struct {
	Common
	Domains Domains
}

// Domains is the policy for hostnames operators add (ADR-0003).
type Domains struct {
	// Preflight requires every A/AAAA record of a new hostname to be one
	// of PublicIPs. Adding a domain fails while it is on and PublicIPs is
	// empty.
	Preflight bool
	PublicIPs []netip.Addr
	// Suffixes, when set, allow only hostnames equal to or below one of them.
	Suffixes []string
}

// Worker configures shipyard-worker.
type Worker struct {
	Common
	WorkerID     string
	PollInterval time.Duration
	// ReconcileInterval is how often expired leases are requeued and Caddy
	// is re-synced from the routes table (so added or removed domains apply).
	ReconcileInterval time.Duration
	// ObservationWindow keeps a superseded deployment's container running
	// after a switch before the reconciler stops it gracefully. 0 stops it
	// at the next reconcile.
	ObservationWindow time.Duration
	// RetainImages is how many earlier releases per app keep their image
	// (rollback targets); 0 keeps only the active release's.
	RetainImages int
	// The retention job runs at start and then every RetentionInterval: it
	// prunes the BuildKit cache down to BuildCacheMax bytes, keeps operation
	// events of each app's last RetainOperations operations only, and
	// trims a finished operation's events to OperationLogMax bytes.
	RetentionInterval time.Duration
	BuildCacheMax     int64
	RetainOperations  int
	OperationLogMax   int64
	// WorkDir holds one checkout per running operation (mode 0700).
	WorkDir string
	// SourceBaseURL is where repositories are cloned from: https, or
	// http on loopback for tests.
	SourceBaseURL string
	// The buildx builder, and its caps, applied when the worker creates it
	// (ADR-0004).
	BuilderName   string
	BuilderMemory string
	BuilderCPUs   float64
	Caddy         Caddy
	Backup        Backup
}

// Backup configures `shipyard-worker backup` (ADR-0006).
type Backup struct {
	// Dir is target A: one directory per backup, holding the database dump
	// and Caddy's data. KEKDir is target B, the KEK files, kept apart so no
	// single location holds both the ciphertext and its keys.
	Dir    string
	KEKDir string
	// Hook and KEKHook are optional shell commands run after each target is
	// written, to copy it off-host; SHIPYARD_BACKUP_PATH names what to copy.
	Hook    string
	KEKHook string
	// PGDump is the pg_dump to run: a name on PATH or an absolute path. Its
	// major version must not be older than the server's [PG-DUMP].
	PGDump string
	// KeepDaily and KeepWeekly bound target A: the newest backup of each of
	// the last KeepDaily days and of each of the last KeepWeekly weeks.
	KeepDaily  int
	KeepWeekly int
}

// Caddy configures the edge container the worker keeps running (ADR-0003).
type Caddy struct {
	Enabled  bool
	Name     string
	Image    string // empty: the pinned default in internal/runtime
	AdminDir string // holds the admin socket; group = the worker's group
	// BindIP is where the ports are published; invalid means all interfaces.
	BindIP              netip.Addr
	HTTPPort, HTTPSPort int // 0 lets Docker choose (tests)
	// CA is "" (Caddy's defaults), "staging" (Let's Encrypt staging), or
	// "internal" (Caddy's local CA, for machines without public DNS).
	CA        string
	ACMEEmail string
}

// LoadAPI reads and validates the API configuration.
func LoadAPI(lookup LookupFunc) (API, error) {
	r := reader{lookup: lookup}
	cfg := API{Common: r.common()}
	cfg.Domains = r.domains()
	return cfg, r.err()
}

func (r *reader) domains() Domains {
	d := Domains{Preflight: r.boolean(EnvDNSPreflight, true)}
	for _, s := range r.list(EnvPublicIPs) {
		ip, err := netip.ParseAddr(s)
		if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			r.fail(EnvPublicIPs, fmt.Errorf("%q is not a public IP address", s))
			continue
		}
		d.PublicIPs = append(d.PublicIPs, ip.Unmap())
	}
	for _, s := range r.list(EnvDomainSuffixes) {
		h := strings.TrimPrefix(strings.ToLower(s), ".")
		if !suffixRE.MatchString(h) {
			r.fail(EnvDomainSuffixes, fmt.Errorf("%q is not a domain suffix such as example.com", s))
			continue
		}
		d.Suffixes = append(d.Suffixes, h)
	}
	return d
}

// list splits a comma-separated value, dropping empty items.
func (r *reader) list(key string) []string {
	var out []string
	for _, s := range strings.Split(r.str(key, ""), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// LoadWorker reads and validates the worker configuration.
func LoadWorker(lookup LookupFunc) (Worker, error) {
	r := reader{lookup: lookup}
	cfg := Worker{
		Common:            r.common(),
		WorkerID:          r.str(EnvWorkerID, ""),
		PollInterval:      r.duration(EnvWorkerPollInterval, DefaultPollInterval),
		ReconcileInterval: r.duration(EnvReconcileInterval, DefaultReconcileInterval),
		WorkDir:           r.str(EnvWorkDir, DefaultWorkDir),
		SourceBaseURL:     r.str(EnvSourceBaseURL, DefaultSourceBaseURL),
		BuilderName:       r.str(EnvBuilderName, DefaultBuilderName),
		BuilderMemory:     strings.ToLower(r.str(EnvBuilderMemory, DefaultBuilderMemory)),
		BuilderCPUs:       DefaultBuilderCPUs,
	}
	if !builderRE.MatchString(cfg.BuilderName) {
		r.fail(EnvBuilderName, fmt.Errorf("%q must be lowercase letters, digits, '-' or '_'", cfg.BuilderName))
	}
	cfg.Caddy = r.caddy()
	// Caddy runs in a container: it reaches the API only through the API's
	// socket, bind-mounted in; the host's loopback is out of its reach.
	if cfg.APIHostname != "" && cfg.Caddy.Enabled {
		switch dir := filepath.Dir(cfg.APISocket()); {
		case cfg.APISocket() == "":
			r.fail(EnvAPIHostname, fmt.Errorf("needs %s=unix:/path.sock, so Caddy can reach the API", EnvAPIListen))
		case dir == cfg.Caddy.AdminDir || dir == "/":
			r.fail(EnvAPIListen, fmt.Errorf("the API socket needs a directory of its own (not / or %s)", EnvCaddyAdminDir))
		}
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = defaultWorkerID()
	}
	if cfg.PollInterval < minPollInterval {
		r.fail(EnvWorkerPollInterval, fmt.Errorf("must be at least %s", minPollInterval))
	}
	if cfg.ReconcileInterval < time.Second {
		r.fail(EnvReconcileInterval, errors.New("must be at least 1s"))
	}
	cfg.ObservationWindow = DefaultObservationWindow
	if s := r.str(EnvObservationWindow, ""); s != "" {
		// Unlike the other durations, 0 is valid: no observation window.
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 || d > 24*time.Hour {
			r.fail(EnvObservationWindow, fmt.Errorf("%q must be a duration between 0s and 24h", s))
		} else {
			cfg.ObservationWindow = d
		}
	}
	cfg.RetainImages = r.intRange(EnvRetainImages, DefaultRetainImages, 0, maxRetainImages)
	cfg.RetainOperations = r.intRange(EnvRetainOperations, DefaultRetainOperations, 1, maxRetainOperations)
	cfg.RetentionInterval = r.duration(EnvRetentionInterval, DefaultRetentionInterval)
	if cfg.RetentionInterval < time.Second {
		r.fail(EnvRetentionInterval, errors.New("must be at least 1s"))
	}
	cfg.BuildCacheMax = r.size(EnvBuildCacheMax, DefaultBuildCacheMax, 1<<20)
	cfg.OperationLogMax = r.size(EnvOperationLogMax, DefaultOperationLogMax, 1<<10)
	cfg.Backup = r.backup(cfg.KEKDir)
	if !filepath.IsAbs(cfg.WorkDir) {
		r.fail(EnvWorkDir, fmt.Errorf("%q must be an absolute path", cfg.WorkDir))
	}
	if u, err := url.Parse(cfg.SourceBaseURL); err != nil || u.User != nil || u.Host == "" ||
		(u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname()))) {
		r.fail(EnvSourceBaseURL, errors.New("must be an https URL without credentials (plain http only on loopback, for tests)"))
	}
	if !memoryRE.MatchString(cfg.BuilderMemory) {
		r.fail(EnvBuilderMemory, fmt.Errorf("%q is not a size like 512m or 2g", cfg.BuilderMemory))
	}
	if s := r.str(EnvBuilderCPUs, ""); s != "" {
		cpus, err := strconv.ParseFloat(s, 64)
		if err != nil || !(cpus >= 0.01 && cpus <= 1024) {
			r.fail(EnvBuilderCPUs, fmt.Errorf("%q must be a number of CPUs between 0.01 and 1024", s))
		} else {
			cfg.BuilderCPUs = cpus
		}
	}
	return cfg, r.err()
}

func (r *reader) backup(kekDir string) Backup {
	b := Backup{
		Dir:        r.str(EnvBackupDir, DefaultBackupDir),
		KEKDir:     r.str(EnvBackupKEKDir, DefaultBackupKEKDir),
		Hook:       r.str(EnvBackupHook, ""),
		KEKHook:    r.str(EnvBackupKEKHook, ""),
		PGDump:     r.str(EnvBackupPGDump, DefaultBackupPGDump),
		KeepDaily:  r.intRange(EnvBackupKeepDaily, DefaultBackupKeepDaily, 1, 366),
		KeepWeekly: r.intRange(EnvBackupKeepWeekly, DefaultBackupKeepWeekly, 0, 520),
	}
	clean := func(key, p string) bool {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			r.fail(key, fmt.Errorf("%q must be a clean absolute path, not /", p))
			return false
		}
		return true
	}
	if okA, okB := clean(EnvBackupDir, b.Dir), clean(EnvBackupKEKDir, b.KEKDir); okA && okB {
		// The two targets, and the live KEK directory, must not contain one
		// another: ciphertext and keys never share a location (ADR-0005).
		switch {
		case within(b.Dir, b.KEKDir) || within(b.KEKDir, b.Dir):
			r.fail(EnvBackupKEKDir, fmt.Errorf("%q and %s=%q must be separate directories", b.KEKDir, EnvBackupDir, b.Dir))
		case within(b.Dir, kekDir) || within(kekDir, b.Dir):
			r.fail(EnvBackupDir, fmt.Errorf("%q must be apart from %s=%q", b.Dir, EnvKEKDir, kekDir))
		case within(b.KEKDir, kekDir) || within(kekDir, b.KEKDir):
			r.fail(EnvBackupKEKDir, fmt.Errorf("%q must be apart from %s=%q", b.KEKDir, EnvKEKDir, kekDir))
		}
	}
	if b.PGDump == "" || (strings.ContainsRune(b.PGDump, '/') && !filepath.IsAbs(b.PGDump)) {
		r.fail(EnvBackupPGDump, fmt.Errorf("%q must be a command name or an absolute path", b.PGDump))
	}
	return b
}

// within reports whether path is dir or lies inside it (clean paths).
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// defaultWorkerID identifies a worker process in operation leases.
func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func (r *reader) caddy() Caddy {
	c := Caddy{
		Enabled:   r.boolean(EnvCaddy, true),
		Name:      r.str(EnvCaddyName, DefaultCaddyName),
		Image:     r.str(EnvCaddyImage, ""),
		AdminDir:  r.str(EnvCaddyAdminDir, DefaultCaddyAdminDir),
		HTTPPort:  r.port(EnvCaddyHTTPPort, 80),
		HTTPSPort: r.port(EnvCaddyHTTPSPort, 443),
		CA:        r.str(EnvCaddyCA, ""),
		ACMEEmail: r.str(EnvACMEEmail, ""),
	}
	if c.CA != "" && c.CA != "staging" && c.CA != "internal" {
		r.fail(EnvCaddyCA, fmt.Errorf("%q must be empty, staging, or internal", c.CA))
	}
	if c.ACMEEmail != "" && (strings.Count(c.ACMEEmail, "@") != 1 || strings.ContainsAny(c.ACMEEmail, " \t\"\\")) {
		r.fail(EnvACMEEmail, fmt.Errorf("%q is not an email address", c.ACMEEmail))
	}
	if !builderRE.MatchString(c.Name) || strings.HasPrefix(c.Name, "shipyard-app-") {
		r.fail(EnvCaddyName, fmt.Errorf("%q must be lowercase letters, digits, '-' or '_', and not an app network name", c.Name))
	}
	if !filepath.IsAbs(c.AdminDir) || filepath.Clean(c.AdminDir) != c.AdminDir || strings.ContainsAny(c.AdminDir, "|:,") {
		r.fail(EnvCaddyAdminDir, fmt.Errorf("%q must be a clean absolute path", c.AdminDir))
	}
	if s := r.str(EnvCaddyBind, ""); s != "" {
		ip, err := netip.ParseAddr(s)
		if err != nil {
			r.fail(EnvCaddyBind, fmt.Errorf("%q is not an IP address", s))
		}
		c.BindIP = ip
	}
	return c
}

// intRange reads a whole number between lo and hi.
func (r *reader) intRange(key string, def, lo, hi int) int {
	s := r.str(key, "")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		r.fail(key, fmt.Errorf("%q must be a number between %d and %d", s, lo, hi))
		return def
	}
	return n
}

// size reads a byte count such as 10g, 512m, or 4096: binary units k, m,
// g, t, an optional trailing b, at least lo and at most 1 PiB.
func (r *reader) size(key string, def, lo int64) int64 {
	s := r.str(key, "")
	if s == "" {
		return def
	}
	if n, ok := parseSize(s); ok && n >= lo {
		return n
	}
	r.fail(key, fmt.Errorf("%q must be a size like 512m or 10g, from %d bytes to 1 PiB", s, lo))
	return def
}

func parseSize(s string) (int64, bool) {
	m := sizeRE.FindStringSubmatch(strings.ToLower(s))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	shift := map[string]uint{"": 0, "k": 10, "m": 20, "g": 30, "t": 40}[m[2]]
	if err != nil || n > maxSize>>shift {
		return 0, false
	}
	return n << shift, true
}

func (r *reader) port(key string, def int) int {
	s := r.str(key, "")
	if s == "" {
		return def
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 0 || p > 65535 {
		r.fail(key, fmt.Errorf("invalid port %q", s))
		return def
	}
	return p
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validateListen enforces that the API is reachable only locally unless the
// operator explicitly opts out: public traffic must arrive through Caddy
// (ARCHITECTURE §7, CLAUDE.md invariant 11).
// validateListen allows loopback TCP or a Unix socket only: the API is
// reached through Caddy over HTTPS, never directly (P2.8).
func validateListen(addr string) error {
	if path, ok := strings.CutPrefix(addr, "unix:"); ok {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "|{} \t\n") {
			return fmt.Errorf("unix socket path %q must be a clean absolute path", path)
		}
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("want host:port or unix:/path: %w", err)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 0 || p > 65535 {
		return fmt.Errorf("invalid port %q", port)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%q is not a loopback address; the API is published through Caddy (%s), never directly", addr, EnvAPIHostname)
}

// reader accumulates every validation error so operators can fix the whole
// environment in one pass.
type reader struct {
	lookup LookupFunc
	errs   []error
}

func (r *reader) fail(key string, err error) {
	r.errs = append(r.errs, fmt.Errorf("%s: %w", key, err))
}

func (r *reader) err() error { return errors.Join(r.errs...) }

func (r *reader) str(key, def string) string {
	if v, ok := r.lookup(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	s := r.str(key, "")
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		r.fail(key, fmt.Errorf("invalid positive duration %q", s))
		return def
	}
	return d
}

func (r *reader) boolean(key string, def bool) bool {
	s := r.str(key, "")
	if s == "" {
		return def
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		r.fail(key, fmt.Errorf("invalid boolean %q", s))
		return def
	}
	return b
}

func (r *reader) common() Common {
	c := Common{
		DatabaseURL:     r.str(EnvDatabaseURL, ""),
		ShutdownTimeout: r.duration(EnvShutdownTimeout, DefaultShutdownTimeout),
		Log:             Log{Level: slog.LevelInfo, Format: logging.FormatJSON},
		KEKDir:          r.str(EnvKEKDir, DefaultKEKDir),
		KEKActive:       r.str(EnvKEKActive, ""),
		WorkerSocket:    r.str(EnvWorkerSocket, DefaultWorkerSocket),
		Listen:          r.str(EnvAPIListen, DefaultAPIListen),
		APIHostname:     strings.TrimSuffix(strings.ToLower(r.str(EnvAPIHostname, "")), "."),
	}
	if c.DatabaseURL == "" {
		r.fail(EnvDatabaseURL, errors.New("required"))
	}
	if err := validateListen(c.Listen); err != nil {
		r.fail(EnvAPIListen, err)
	}
	// Same rules as an app hostname (the routes.hostname CHECK).
	if c.APIHostname != "" && (!suffixRE.MatchString(c.APIHostname) || len(c.APIHostname) > 253) {
		r.fail(EnvAPIHostname, fmt.Errorf("%q is not a hostname such as shipyard.example.com", c.APIHostname))
	}
	if !filepath.IsAbs(c.WorkerSocket) || filepath.Clean(c.WorkerSocket) != c.WorkerSocket {
		r.fail(EnvWorkerSocket, fmt.Errorf("%q must be a clean absolute path", c.WorkerSocket))
	}
	if !filepath.IsAbs(c.KEKDir) {
		r.fail(EnvKEKDir, fmt.Errorf("%q must be an absolute path", c.KEKDir))
	}
	if c.KEKActive != "" && !kekIDRE.MatchString(c.KEKActive) {
		r.fail(EnvKEKActive, fmt.Errorf("%q must be 1-64 characters of A-Z, a-z, 0-9, '_', '-'", c.KEKActive))
	}
	if s := r.str(EnvLogLevel, ""); s != "" {
		if err := c.Log.Level.UnmarshalText([]byte(s)); err != nil {
			r.fail(EnvLogLevel, fmt.Errorf("invalid level %q (want debug, info, warn or error)", s))
		}
	}
	if s := r.str(EnvLogFormat, ""); s != "" {
		f, err := logging.ParseFormat(s)
		if err != nil {
			r.fail(EnvLogFormat, err)
		} else {
			c.Log.Format = f
		}
	}
	return c
}
