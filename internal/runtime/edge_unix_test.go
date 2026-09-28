//go:build unix

package runtime

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// A recreated edge starts without the sockets of the old one (admin and
// verify), whose group may be stale; other files are left alone.
func TestRemoveSockets(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"caddy-admin.sock", "caddy-verify.sock"} {
		ln, err := net.Listen("unix", filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		// Keep the file, as a killed Caddy does.
		ln.(*net.UnixListener).SetUnlinkOnClose(false)
		ln.Close()
	}
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeSockets(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "notes.txt" {
		t.Fatalf("left = %v, %v", entries, err)
	}
	if err := removeSockets(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing directory: no error")
	}
}
