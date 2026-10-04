package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/secrets"
)

// ADR-0012: kek generate writes a private key only its owner can read and
// a public key, which load as one KEK; an ID is never reused.
func TestKEKGenerate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode checks are POSIX-only")
	}
	dir := t.TempDir()
	var out bytes.Buffer
	if err := kekGenerate(dir, "a1", &out); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"a1.hpke": 0o600, "a1.pub": 0o644} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil || fi.Mode().Perm()&^want != 0 || fi.Size() != 32 {
			t.Fatalf("%s: %v, %v", name, fi, err)
		}
	}
	if !strings.Contains(out.String(), "SHIPYARD_KEK_ACTIVE=a1") || !strings.Contains(out.String(), "kek rewrap") {
		t.Fatalf("instructions: %s", out.String())
	}
	k, err := secrets.LoadKeyring(dir, "a1")
	if err != nil || !k.CanOpen("a1") {
		t.Fatalf("load the generated KEK: %v", err)
	}
	if api, err := secrets.LoadSealKeyring(dir, "a1"); err != nil || api.CanOpen("a1") {
		t.Fatalf("seal-only load: %v", err)
	}

	// Negative: an existing ID, of any kind, or an invalid one.
	os.WriteFile(filepath.Join(dir, "k1.key"), secrets.GenerateKey(), 0o600)
	for id, want := range map[string]string{"a1": "already exists", "k1": "already exists", "a 1": "invalid KEK id", "": "invalid KEK id", "../x": "invalid KEK id"} {
		if err := kekGenerate(dir, id, &out); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("generate %q = %v, want %q", id, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "k1.hpke")); err == nil {
		t.Fatal("a refused generate wrote a file")
	}
}
