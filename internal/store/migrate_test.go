package store

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadMigrations(t *testing.T) {
	fsys := fstest.MapFS{
		"0002_second.sql": {Data: []byte("SELECT 2;")},
		"0001_first.sql":  {Data: []byte("SELECT 1;")},
		"README.md":       {Data: []byte("ignored")},
		"migrations.go":   {Data: []byte("package migrations")},
	}
	got, err := LoadMigrations(fsys)
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(got) != 2 || got[0].Version != 1 || got[0].Name != "first" || got[1].Version != 2 || got[1].SQL != "SELECT 2;" {
		t.Errorf("got %+v", got)
	}
}

func TestLoadMigrationsRejectsBadSets(t *testing.T) {
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		wantErr string
	}{
		{"bad name", fstest.MapFS{"1_init.sql": {Data: []byte("SELECT 1;")}}, "must match"},
		{"uppercase name", fstest.MapFS{"0001_Init.sql": {Data: []byte("SELECT 1;")}}, "must match"},
		{"zero version", fstest.MapFS{"0000_init.sql": {Data: []byte("SELECT 1;")}}, "expected 0001"},
		{"gap", fstest.MapFS{
			"0001_a.sql": {Data: []byte("SELECT 1;")},
			"0003_c.sql": {Data: []byte("SELECT 1;")},
		}, "expected 0002"},
		{"duplicate version", fstest.MapFS{
			"0001_a.sql": {Data: []byte("SELECT 1;")},
			"0001_b.sql": {Data: []byte("SELECT 1;")},
		}, "expected 0002"},
		{"empty file", fstest.MapFS{"0001_a.sql": {Data: []byte("  \n")}}, "is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadMigrations(tt.fsys)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestOpenRedactsPassword(t *testing.T) {
	const secret = "s3cr3t-value"
	urls := []string{
		"postgres://shipyard:" + secret + "@127.0.0.1:notaport/shipyard",
		"host=127.0.0.1 port=notaport user=shipyard password=" + secret,
	}
	for _, u := range urls {
		_, err := Open(context.Background(), u)
		if err == nil {
			t.Fatalf("Open(%q) succeeded, want parse error", u)
		}
		t.Logf("error: %v", err)
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks the password: %v", err)
		}
	}
}
