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
