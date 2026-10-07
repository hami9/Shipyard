package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Environment variables that override the config file.
const (
	envConfig = "SHIPYARD_CONFIG" // path to the config file
	envURL    = "SHIPYARD_URL"
	envToken  = "SHIPYARD_TOKEN"
)

// config is the CLI's saved connection: the API URL and a token. The token
// is a credential, so the file is private to the user (mode 0600).
type config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func defaultConfigPath(getenv func(string) string) (string, error) {
	if p := getenv(envConfig); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w (set %s)", err, envConfig)
	}
	return filepath.Join(dir, "shipyard", "config.json"), nil
}

// loadConfig reads the file if it exists, then applies SHIPYARD_URL and
// SHIPYARD_TOKEN, which suit CI jobs.
func loadConfig(path string, getenv func(string) string) (config, error) {
	c, err := readConfigFile(path)
	if err != nil {
		return c, err
	}
	if v := getenv(envURL); v != "" {
		c.URL = v
	}
	if v := getenv(envToken); v != "" {
		c.Token = v
	}
	if c.URL == "" {
		return c, fmt.Errorf("no API URL configured; run `shipyard login --url URL` or set %s", envURL)
	}
	return c, nil
}

// readConfigFile reads the file alone, without the environment overrides;
// a missing file is an empty config.
func readConfigFile(path string) (config, error) {
	var c config
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, err
	default:
		if err := checkPrivate(path); err != nil {
			return c, err
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	return c, nil
}

// checkPrivate refuses a config file other users can read: it holds a token.
func checkPrivate(path string) error {
	if runtime.GOOS == "windows" {
		return nil // POSIX modes do not apply; the profile directory is per-user
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by other users (mode %v); run: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	return nil
}

// saveConfig writes the file atomically with mode 0600.
func saveConfig(path string, c config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
