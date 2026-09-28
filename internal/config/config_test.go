package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/logging"
)

func env(kv map[string]string) LookupFunc {
	return func(k string) (string, bool) {
		v, ok := kv[k]
		return v, ok
	}
}

const testDB = "postgres://shipyard:pw@127.0.0.1:5432/shipyard"

func TestLoadAPIDefaults(t *testing.T) {
	cfg, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if cfg.Listen != DefaultAPIListen {
		t.Errorf("Listen = %q, want %q", cfg.Listen, DefaultAPIListen)
	}
	if cfg.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
	if cfg.Log.Level != slog.LevelInfo || cfg.Log.Format != logging.FormatJSON {
		t.Errorf("Log = %+v, want info/json", cfg.Log)
	}
}

func TestLoadAPIOverrides(t *testing.T) {
	cfg, err := LoadAPI(env(map[string]string{
		EnvDatabaseURL:     testDB,
		EnvLogLevel:        "debug",
		EnvLogFormat:       "text",
		EnvShutdownTimeout: "3s",
		EnvAPIListen:       "unix:/run/shipyard/api.sock",
	}))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if cfg.Log.Level != slog.LevelDebug || cfg.Log.Format != logging.FormatText {
		t.Errorf("Log = %+v", cfg.Log)
	}
	if cfg.ShutdownTimeout != 3*time.Second || cfg.Listen != "unix:/run/shipyard/api.sock" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadKEK(t *testing.T) {
	cfg, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil || cfg.KEKDir != DefaultKEKDir || cfg.KEKActive != "" {
		t.Fatalf("defaults: %+v, %v", cfg.Common, err)
	}
	w, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvKEKDir: "/srv/kek", EnvKEKActive: "2026-09_a"}))
	if err != nil || w.KEKDir != "/srv/kek" || w.KEKActive != "2026-09_a" {
		t.Fatalf("overrides: %+v, %v", w.Common, err)
	}
	_, err = LoadAPI(env(map[string]string{EnvDatabaseURL: testDB, EnvKEKDir: "kek", EnvKEKActive: "bad id"}))
	if err == nil || !strings.Contains(err.Error(), EnvKEKDir) || !strings.Contains(err.Error(), EnvKEKActive) {
		t.Fatalf("want both KEK errors, got %v", err)
	}
}

// P2.8: loopback or a Unix socket only; there is no override any more.
func TestLoadAPIListenValidation(t *testing.T) {
	tests := []struct {
		listen  string
		wantErr string
	}{
		{"127.0.0.1:8080", ""},
		{"localhost:9000", ""},
		{"[::1]:8080", ""},
		{"unix:/run/shipyard/api.sock", ""},
		{"0.0.0.0:8080", "not a loopback"},
		{":8080", "not a loopback"},
		{"10.0.0.5:8080", "not a loopback"},
		{"api.example.com:8080", "not a loopback"},
		{"unix:relative.sock", "clean absolute path"},
		{"unix:/run/../api.sock", "clean absolute path"},
		{"unix:/run/a b.sock", "clean absolute path"},
		{"127.0.0.1", "host:port"},
		{"127.0.0.1:http", "invalid port"},
	}
	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			_, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB, EnvAPIListen: tt.listen}))
			checkErr(t, err, tt.wantErr)
		})
	}
	// The removed override no longer opens a public address.
	_, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB, EnvAPIListen: "0.0.0.0:8080", "SHIPYARD_API_ALLOW_PUBLIC_LISTEN": "true"}))
	checkErr(t, err, "not a loopback")
}

// P2.8: the API hostname Caddy publishes the API on; the worker needs the
// API on a Unix socket in a directory of its own.
func TestAPIHostname(t *testing.T) {
	const sock = "unix:/run/shipyard-api/api.sock"
	cfg, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvAPIHostname: "Shipyard.Example.com.", EnvAPIListen: sock}))
	if err != nil || cfg.APIHostname != "shipyard.example.com" || cfg.APISocket() != "/run/shipyard-api/api.sock" {
		t.Fatalf("worker = %q %q, %v", cfg.APIHostname, cfg.APISocket(), err)
	}
	if a, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB, EnvAPIHostname: "shipyard.example.com"})); err != nil || a.APIHostname != "shipyard.example.com" || a.APISocket() != "" {
		t.Fatalf("api = %+v, %v", a.Common, err)
	}
	// Caddy disabled: nothing to publish, so TCP is fine.
	if _, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvAPIHostname: "shipyard.example.com", EnvCaddy: "false"})); err != nil {
		t.Fatalf("caddy off: %v", err)
	}
	for name, kv := range map[string]map[string]string{
		"tcp listen":       {EnvAPIHostname: "shipyard.example.com"},
		"socket in /":      {EnvAPIHostname: "shipyard.example.com", EnvAPIListen: "unix:/api.sock"},
		"admin dir":        {EnvAPIHostname: "shipyard.example.com", EnvAPIListen: "unix:/srv/caddy/api.sock", EnvCaddyAdminDir: "/srv/caddy"},
		"single label":     {EnvAPIHostname: "shipyard", EnvAPIListen: sock},
		"wildcard":         {EnvAPIHostname: "*.example.com", EnvAPIListen: sock},
		"ip address":       {EnvAPIHostname: "203.0.113.7", EnvAPIListen: sock},
		"port in the name": {EnvAPIHostname: "shipyard.example.com:443", EnvAPIListen: sock},
	} {
		t.Run(name, func(t *testing.T) {
			kv[EnvDatabaseURL] = testDB
			_, err := LoadWorker(env(kv))
			if err == nil || (!strings.Contains(err.Error(), EnvAPIHostname) && !strings.Contains(err.Error(), EnvAPIListen)) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestLoadCommonErrors(t *testing.T) {
	tests := []struct {
		name    string
		kv      map[string]string
		wantErr string
	}{
		{"missing database url", map[string]string{}, EnvDatabaseURL + ": required"},
		{"blank database url", map[string]string{EnvDatabaseURL: "   "}, EnvDatabaseURL + ": required"},
		{"bad level", map[string]string{EnvDatabaseURL: testDB, EnvLogLevel: "loud"}, "invalid level"},
		{"bad format", map[string]string{EnvDatabaseURL: testDB, EnvLogFormat: "xml"}, "unknown log format"},
		{"bad duration", map[string]string{EnvDatabaseURL: testDB, EnvShutdownTimeout: "soon"}, "invalid positive duration"},
		{"negative duration", map[string]string{EnvDatabaseURL: testDB, EnvShutdownTimeout: "-1s"}, "invalid positive duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadAPI(env(tt.kv))
			checkErr(t, err, tt.wantErr)
		})
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := LoadAPI(env(map[string]string{EnvLogLevel: "loud", EnvAPIListen: "0.0.0.0:80"}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{EnvDatabaseURL, EnvLogLevel, EnvAPIListen} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestLoadWorker(t *testing.T) {
	cfg, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil {
		t.Fatalf("LoadWorker: %v", err)
	}
	if cfg.WorkerID == "" {
		t.Error("default WorkerID is empty")
	}
	if cfg.PollInterval != DefaultPollInterval {
		t.Errorf("PollInterval = %s", cfg.PollInterval)
	}

	cfg, err = LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvWorkerID: "w1", EnvWorkerPollInterval: "500ms"}))
	if err != nil {
		t.Fatalf("LoadWorker: %v", err)
	}
	if cfg.WorkerID != "w1" || cfg.PollInterval != 500*time.Millisecond {
		t.Errorf("cfg = %+v", cfg)
	}

	_, err = LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvWorkerPollInterval: "10ms"}))
	checkErr(t, err, "at least")
}

func TestLoadAPIDomains(t *testing.T) {
	cfg, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil || !cfg.Domains.Preflight || cfg.Domains.PublicIPs != nil || cfg.Domains.Suffixes != nil {
		t.Fatalf("defaults = %+v, %v", cfg.Domains, err)
	}
	cfg, err = LoadAPI(env(map[string]string{EnvDatabaseURL: testDB, EnvDNSPreflight: "false",
		EnvPublicIPs: " 203.0.113.10, 2001:db8::10 ,", EnvDomainSuffixes: "Example.com, .apps.example.org"}))
	d := cfg.Domains
	if err != nil || d.Preflight || len(d.PublicIPs) != 2 || d.PublicIPs[1].String() != "2001:db8::10" ||
		strings.Join(d.Suffixes, ",") != "example.com,apps.example.org" {
		t.Fatalf("cfg = %+v, %v", d, err)
	}
	for name, tc := range map[string]struct{ key, value string }{
		"private IP":        {EnvPublicIPs, "10.0.0.5"},
		"loopback IP":       {EnvPublicIPs, "127.0.0.1"},
		"not an IP":         {EnvPublicIPs, "vps.example.com"},
		"wildcard suffix":   {EnvDomainSuffixes, "*.example.com"},
		"single label":      {EnvDomainSuffixes, "com"},
		"preflight garbage": {EnvDNSPreflight, "sometimes"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB, tc.key: tc.value}))
			checkErr(t, err, tc.key)
		})
	}
}

func TestLoadWorkerReconcileInterval(t *testing.T) {
	cfg, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil || cfg.ReconcileInterval != time.Minute {
		t.Fatalf("default = %s, %v", cfg.ReconcileInterval, err)
	}
	cfg, err = LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvReconcileInterval: "2s"}))
	if err != nil || cfg.ReconcileInterval != 2*time.Second {
		t.Fatalf("2s = %s, %v", cfg.ReconcileInterval, err)
	}
	_, err = LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvReconcileInterval: "10ms"}))
	checkErr(t, err, EnvReconcileInterval)
}

func TestWorkerSocket(t *testing.T) {
	cfg, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil || cfg.WorkerSocket != DefaultWorkerSocket {
		t.Fatalf("default = %q, %v", cfg.WorkerSocket, err)
	}
	w, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvWorkerSocket: "/tmp/x/logs.sock"}))
	if err != nil || w.WorkerSocket != "/tmp/x/logs.sock" {
		t.Fatalf("set = %q, %v", w.WorkerSocket, err)
	}
	for _, bad := range []string{"logs.sock", "/run/../logs.sock", "/run//logs.sock"} {
		_, err := LoadAPI(env(map[string]string{EnvDatabaseURL: testDB, EnvWorkerSocket: bad}))
		checkErr(t, err, EnvWorkerSocket)
	}
}

func TestLoadWorkerObservationWindow(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"", 5 * time.Minute}, {"0", 0}, {"0s", 0}, {"90s", 90 * time.Second}, {"24h", 24 * time.Hour}} {
		cfg, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvObservationWindow: tc.value}))
		if err != nil || cfg.ObservationWindow != tc.want {
			t.Errorf("%q = %s, %v; want %s", tc.value, cfg.ObservationWindow, err, tc.want)
		}
	}
	for _, bad := range []string{"-1s", "soon", "25h"} {
		_, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvObservationWindow: bad}))
		checkErr(t, err, EnvObservationWindow)
	}
}

func TestLoadWorkerCaddy(t *testing.T) {
	cfg, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Caddy
	if !c.Enabled || c.Name != "shipyard-caddy" || c.Image != "" || c.AdminDir != "/run/shipyard/caddy" ||
		c.Group != "shipyard-edge" || c.BindIP.IsValid() || c.HTTPPort != 80 || c.HTTPSPort != 443 {
		t.Errorf("defaults = %+v", c)
	}
	cfg, err = LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvCaddy: "false", EnvCaddyName: "edge-2",
		EnvCaddyImage: "caddy:2", EnvCaddyAdminDir: "/srv/caddy", EnvCaddyGroup: "1234", EnvCaddyBind: "127.0.0.1", EnvCaddyHTTPPort: "0", EnvCaddyHTTPSPort: "8443",
		EnvCaddyCA: "staging", EnvACMEEmail: "ops@example.com"}))
	c = cfg.Caddy
	if err != nil || c.Enabled || c.Name != "edge-2" || c.Image != "caddy:2" || c.AdminDir != "/srv/caddy" || c.Group != "1234" ||
		c.BindIP.String() != "127.0.0.1" || c.HTTPPort != 0 || c.HTTPSPort != 8443 || c.CA != "staging" || c.ACMEEmail != "ops@example.com" {
		t.Fatalf("cfg = %+v, %v", c, err)
	}
	for name, tc := range map[string]struct{ key, value string }{
		"app network name":    {EnvCaddyName, "shipyard-app-x"},
		"bad name":            {EnvCaddyName, "Caddy!"},
		"relative admin dir":  {EnvCaddyAdminDir, "run/caddy"},
		"permission suffix":   {EnvCaddyAdminDir, "/run/caddy|0777"},
		"group with space":    {EnvCaddyGroup, "shipyard edge"},
		"uppercase group":     {EnvCaddyGroup, "Edge"},
		"negative gid":        {EnvCaddyGroup, "-1"},
		"bind not an ip":      {EnvCaddyBind, "localhost"},
		"port out of range":   {EnvCaddyHTTPPort, "70000"},
		"port not a number":   {EnvCaddyHTTPSPort, "https"},
		"enabled not boolean": {EnvCaddy, "maybe"},
		"unknown CA":          {EnvCaddyCA, "zerossl"},
		"bad email":           {EnvACMEEmail, "ops at example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, tc.key: tc.value}))
			checkErr(t, err, tc.key)
		})
	}
}

func TestLoadWorkerDeploySettings(t *testing.T) {
	cfg, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkDir != DefaultWorkDir || cfg.SourceBaseURL != "https://github.com" || cfg.BuilderMemory != "2g" || cfg.BuilderCPUs != 2 {
		t.Errorf("defaults = %+v", cfg)
	}
	cfg, err = LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvWorkDir: "/srv/work",
		EnvSourceBaseURL: "http://127.0.0.1:9999", EnvBuilderMemory: "512M", EnvBuilderCPUs: "0.5"}))
	if err != nil || cfg.WorkDir != "/srv/work" || cfg.SourceBaseURL != "http://127.0.0.1:9999" || cfg.BuilderMemory != "512m" || cfg.BuilderCPUs != 0.5 {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	for name, tc := range map[string]struct{ key, value, want string }{
		"relative work dir":    {EnvWorkDir, "work", EnvWorkDir},
		"plain http remote":    {EnvSourceBaseURL, "http://git.example.com", EnvSourceBaseURL},
		"credentials in url":   {EnvSourceBaseURL, "https://user:pw@github.com", EnvSourceBaseURL},
		"other scheme":         {EnvSourceBaseURL, "file:///srv/repos", EnvSourceBaseURL},
		"builder name":         {EnvBuilderName, "Shipyard; rm", EnvBuilderName},
		"bad memory":           {EnvBuilderMemory, "lots", EnvBuilderMemory},
		"memory with fraction": {EnvBuilderMemory, "1.5g", EnvBuilderMemory},
		"zero cpus":            {EnvBuilderCPUs, "0", EnvBuilderCPUs},
		"cpus not a number":    {EnvBuilderCPUs, "two", EnvBuilderCPUs},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, tc.key: tc.value}))
			checkErr(t, err, tc.want)
			if err != nil && strings.Contains(err.Error(), "pw@") {
				t.Errorf("error leaks the URL's credentials: %v", err)
			}
		})
	}
}

func checkErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("unexpected error: %v", err)
	case want != "" && err == nil:
		t.Errorf("want error containing %q, got nil", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Errorf("error %q does not contain %q", err, want)
	}
}
