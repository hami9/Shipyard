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
	"os"
	"path/filepath"
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
	EnvAPIAllowPublic     = "SHIPYARD_API_ALLOW_PUBLIC_LISTEN"
	EnvWorkerID           = "SHIPYARD_WORKER_ID"
	EnvWorkerPollInterval = "SHIPYARD_WORKER_POLL_INTERVAL"
)

// Defaults.
const (
	DefaultAPIListen       = "127.0.0.1:8080"
	DefaultShutdownTimeout = 15 * time.Second
	DefaultPollInterval    = 2 * time.Second
	minPollInterval        = 100 * time.Millisecond
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
}

// API configures shipyard-api.
type API struct {
	Common
	// Listen is host:port or unix:/absolute/path.sock.
	Listen string
}

// Worker configures shipyard-worker.
type Worker struct {
	Common
	WorkerID     string
	PollInterval time.Duration
}

// LoadAPI reads and validates the API configuration.
func LoadAPI(lookup LookupFunc) (API, error) {
	r := reader{lookup: lookup}
	cfg := API{
		Common: r.common(),
		Listen: r.str(EnvAPIListen, DefaultAPIListen),
	}
	allowPublic := r.boolean(EnvAPIAllowPublic, false)
	if err := validateListen(cfg.Listen, allowPublic); err != nil {
		r.fail(EnvAPIListen, err)
	}
	return cfg, r.err()
}

// LoadWorker reads and validates the worker configuration.
func LoadWorker(lookup LookupFunc) (Worker, error) {
	r := reader{lookup: lookup}
	cfg := Worker{
		Common:       r.common(),
		WorkerID:     r.str(EnvWorkerID, ""),
		PollInterval: r.duration(EnvWorkerPollInterval, DefaultPollInterval),
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = defaultWorkerID()
	}
	if cfg.PollInterval < minPollInterval {
		r.fail(EnvWorkerPollInterval, fmt.Errorf("must be at least %s", minPollInterval))
	}
	return cfg, r.err()
}

// defaultWorkerID identifies a worker process in operation leases.
func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// validateListen enforces that the API is reachable only locally unless the
// operator explicitly opts out: public traffic must arrive through Caddy
// (ARCHITECTURE §7, CLAUDE.md invariant 11).
func validateListen(addr string, allowPublic bool) error {
	if path, ok := strings.CutPrefix(addr, "unix:"); ok {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("unix socket path %q must be absolute", path)
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
	if allowPublic || host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%q is not a loopback address; the API must sit behind Caddy (set %s=true to override)", addr, EnvAPIAllowPublic)
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
	}
	if c.DatabaseURL == "" {
		r.fail(EnvDatabaseURL, errors.New("required"))
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
