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

func TestLoadAPIListenValidation(t *testing.T) {
	tests := []struct {
		listen      string
		allowPublic string
		wantErr     string
	}{
		{"127.0.0.1:8080", "", ""},
		{"localhost:9000", "", ""},
		{"[::1]:8080", "", ""},
		{"unix:/run/shipyard/api.sock", "", ""},
		{"0.0.0.0:8080", "", "not a loopback"},
		{":8080", "", "not a loopback"},
		{"10.0.0.5:8080", "", "not a loopback"},
		{"api.example.com:8080", "", "not a loopback"},
		{"10.0.0.5:8080", "true", ""},
		{"unix:relative.sock", "", "must be absolute"},
		{"127.0.0.1", "", "host:port"},
		{"127.0.0.1:http", "", "invalid port"},
		{"127.0.0.1:8080", "maybe", "invalid boolean"},
	}
	for _, tt := range tests {
		t.Run(tt.listen+"/"+tt.allowPublic, func(t *testing.T) {
			kv := map[string]string{EnvDatabaseURL: testDB, EnvAPIListen: tt.listen}
			if tt.allowPublic != "" {
				kv[EnvAPIAllowPublic] = tt.allowPublic
			}
			_, err := LoadAPI(env(kv))
			checkErr(t, err, tt.wantErr)
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

func TestLoadWorkerCaddy(t *testing.T) {
	cfg, err := LoadWorker(env(map[string]string{EnvDatabaseURL: testDB}))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Caddy
	if !c.Enabled || c.Name != "shipyard-caddy" || c.Image != "" || c.AdminDir != "/run/shipyard/caddy" ||
		c.BindIP.IsValid() || c.HTTPPort != 80 || c.HTTPSPort != 443 {
		t.Errorf("defaults = %+v", c)
	}
	cfg, err = LoadWorker(env(map[string]string{EnvDatabaseURL: testDB, EnvCaddy: "false", EnvCaddyName: "edge-2",
		EnvCaddyImage: "caddy:2", EnvCaddyAdminDir: "/srv/caddy", EnvCaddyBind: "127.0.0.1", EnvCaddyHTTPPort: "0", EnvCaddyHTTPSPort: "8443"}))
	c = cfg.Caddy
	if err != nil || c.Enabled || c.Name != "edge-2" || c.Image != "caddy:2" || c.AdminDir != "/srv/caddy" ||
		c.BindIP.String() != "127.0.0.1" || c.HTTPPort != 0 || c.HTTPSPort != 8443 {
		t.Fatalf("cfg = %+v, %v", c, err)
	}
	for name, tc := range map[string]struct{ key, value string }{
		"app network name":    {EnvCaddyName, "shipyard-app-x"},
		"bad name":            {EnvCaddyName, "Caddy!"},
		"relative admin dir":  {EnvCaddyAdminDir, "run/caddy"},
		"permission suffix":   {EnvCaddyAdminDir, "/run/caddy|0777"},
		"bind not an ip":      {EnvCaddyBind, "localhost"},
		"port out of range":   {EnvCaddyHTTPPort, "70000"},
		"port not a number":   {EnvCaddyHTTPSPort, "https"},
		"enabled not boolean": {EnvCaddy, "maybe"},
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
