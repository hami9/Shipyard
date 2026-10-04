package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

const (
	appA = "11111111-1111-4111-8111-111111111111"
	appB = "22222222-2222-4222-8222-222222222222"
)

func testKeyring(t *testing.T, active string, ids ...string) (*Keyring, map[string][]byte) {
	t.Helper()
	keys := map[string][]byte{}
	for _, id := range ids {
		keys[id] = GenerateKey()
	}
	k, err := NewKeyring(active, keys)
	if err != nil {
		t.Fatal(err)
	}
	return k, keys
}

func TestSealOpenRoundTrip(t *testing.T) {
	k, _ := testKeyring(t, "k1", "k1")
	for _, plain := range [][]byte{[]byte("postgres://u:p@db/app"), {}, bytes.Repeat([]byte{0}, 4096)} {
		id := NewValueID()
		s, err := k.Seal(appA, "DATABASE_URL", id, plain)
		if err != nil {
			t.Fatal(err)
		}
		if s.KEKID != "k1" || s.ValueID != id || len(s.Ciphertext) != len(plain)+28 || len(s.WrappedDEK) != KeySize+28 {
			t.Fatalf("sealed shape: %+v", s)
		}
		if len(plain) > 0 && bytes.Contains(s.Ciphertext, plain) {
			t.Fatal("ciphertext contains the plaintext")
		}
		got, err := k.Open(appA, "DATABASE_URL", s)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("Open = %q, %v", got, err)
		}
	}
	a, _ := k.Seal(appA, "K", "v", []byte("same"))
	b, _ := k.Seal(appA, "K", "v", []byte("same"))
	if bytes.Equal(a.Ciphertext, b.Ciphertext) || bytes.Equal(a.WrappedDEK, b.WrappedDEK) {
		t.Fatal("two seals of the same value are identical: nonce or DEK reuse")
	}
}

// The AAD binds app, key, and row; a ciphertext moved anywhere else fails.
func TestOpenWrongAAD(t *testing.T) {
	k, _ := testKeyring(t, "k1", "k1")
	s, _ := k.Seal(appA, "TOKEN", NewValueID(), []byte("s3cret"))
	other, _ := k.Seal(appA, "TOKEN", NewValueID(), []byte("other"))

	cases := map[string]struct {
		app, key string
		sealed   Sealed
	}{
		"other app":               {appB, "TOKEN", s},
		"other key":               {appA, "API_TOKEN", s},
		"other row id":            {appA, "TOKEN", Sealed{ValueID: other.ValueID, Ciphertext: s.Ciphertext, WrappedDEK: s.WrappedDEK, KEKID: "k1"}},
		"DEK from another row":    {appA, "TOKEN", Sealed{ValueID: s.ValueID, Ciphertext: s.Ciphertext, WrappedDEK: other.WrappedDEK, KEKID: "k1"}},
		"ciphertext from another": {appA, "TOKEN", Sealed{ValueID: s.ValueID, Ciphertext: other.Ciphertext, WrappedDEK: s.WrappedDEK, KEKID: "k1"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := k.Open(c.app, c.key, c.sealed); !errors.Is(err, ErrDecrypt) || got != nil {
				t.Fatalf("Open = %q, %v; want ErrDecrypt", got, err)
			}
		})
	}
}

func TestOpenWrongKEK(t *testing.T) {
	k1, _ := testKeyring(t, "k1", "k1")
	impostor, _ := testKeyring(t, "k1", "k1") // same id, different key
	s, _ := k1.Seal(appA, "K", NewValueID(), []byte("v"))
	if _, err := impostor.Open(appA, "K", s); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong KEK: %v", err)
	}
	s.KEKID = "k9"
	if _, err := k1.Open(appA, "K", s); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unknown KEK id: %v", err)
	}
}

func TestOpenTampered(t *testing.T) {
	k, _ := testKeyring(t, "k1", "k1")
	s, _ := k.Seal(appA, "K", NewValueID(), []byte("a value long enough to flip many bytes"))
	flip := func(b []byte, i int) []byte { c := bytes.Clone(b); c[i] ^= 0x01; return c }
	for i := range s.Ciphertext {
		t2 := s
		t2.Ciphertext = flip(s.Ciphertext, i)
		if _, err := k.Open(appA, "K", t2); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("ciphertext byte %d flipped: %v", i, err)
		}
	}
	for i := range s.WrappedDEK {
		t2 := s
		t2.WrappedDEK = flip(s.WrappedDEK, i)
		if _, err := k.Open(appA, "K", t2); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("wrapped DEK byte %d flipped: %v", i, err)
		}
	}
	for _, short := range []Sealed{{ValueID: s.ValueID, KEKID: "k1"}, {ValueID: s.ValueID, KEKID: "k1", WrappedDEK: s.WrappedDEK, Ciphertext: s.Ciphertext[:27]}} {
		if _, err := k.Open(appA, "K", short); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("truncated input: %v", err)
		}
	}
}

// Rotation: a keyring with a new active KEK still opens values sealed under
// the old one, and seals new values with the new one.
func TestKeyRotation(t *testing.T) {
	keys := map[string][]byte{"k1": GenerateKey(), "k2": GenerateKey()}
	old, _ := NewKeyring("k1", map[string][]byte{"k1": keys["k1"]})
	s1, _ := old.Seal(appA, "K", NewValueID(), []byte("before"))
	rotated, err := NewKeyring("k2", keys)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.Open(appA, "K", s1); err != nil || string(got) != "before" {
		t.Fatalf("open old value after rotation: %q, %v", got, err)
	}
	if s2, _ := rotated.Seal(appA, "K", NewValueID(), []byte("after")); s2.KEKID != "k2" {
		t.Fatalf("new value sealed with %s, want k2", s2.KEKID)
	}
}

// ADR-0012: a re-wrapped value opens with the new KEK alone, its ciphertext
// unchanged; a re-wrap needs the right KEK, ID, and wrapping.
func TestRewrap(t *testing.T) {
	keys := map[string][]byte{"k1": GenerateKey(), "k2": GenerateKey()}
	old, _ := NewKeyring("k1", map[string][]byte{"k1": keys["k1"]})
	id := NewValueID()
	s, _ := old.Seal(appA, "K", id, []byte("value"))
	both, _ := NewKeyring("k2", keys)
	if both.Active() != "k2" || !both.Has("k1") || both.Has("k3") {
		t.Fatalf("Active/Has: %s", both.Active())
	}
	wrapped, err := both.Rewrap(id, s.WrappedDEK, "k1")
	if err != nil || bytes.Equal(wrapped, s.WrappedDEK) {
		t.Fatalf("Rewrap = %x, %v", wrapped, err)
	}
	onlyNew, _ := NewKeyring("k2", map[string][]byte{"k2": keys["k2"]})
	moved := Sealed{ValueID: id, Ciphertext: s.Ciphertext, WrappedDEK: wrapped, KEKID: "k2"}
	if got, err := onlyNew.Open(appA, "K", moved); err != nil || string(got) != "value" {
		t.Fatalf("open after rewrap with the new KEK only: %q, %v", got, err)
	}

	// Negative: a wrapping only opens under its own KEK and value ID.
	for name, try := range map[string]func() ([]byte, error){
		"unknown KEK":    func() ([]byte, error) { return onlyNew.Rewrap(id, s.WrappedDEK, "k1") },
		"wrong KEK":      func() ([]byte, error) { return both.Rewrap(id, s.WrappedDEK, "k2") },
		"other value ID": func() ([]byte, error) { return both.Rewrap(NewValueID(), s.WrappedDEK, "k1") },
		"tampered": func() ([]byte, error) {
			b := bytes.Clone(s.WrappedDEK)
			b[len(b)-1] ^= 1
			return both.Rewrap(id, b, "k1")
		},
	} {
		if _, err := try(); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: err = %v, want ErrDecrypt", name, err)
		}
	}
	// The re-wrapped key is bound to the new KEK's ID: relabelling fails.
	if _, err := onlyNew.Open(appA, "K", Sealed{ValueID: id, Ciphertext: s.Ciphertext, WrappedDEK: s.WrappedDEK, KEKID: "k2"}); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("old wrapping relabelled as k2 opened: %v", err)
	}
}

func TestNewKeyringRejects(t *testing.T) {
	good := GenerateKey()
	cases := map[string]struct {
		active string
		keys   map[string][]byte
	}{
		"short key":      {"k1", map[string][]byte{"k1": good[:16]}},
		"long key":       {"k1", map[string][]byte{"k1": append(bytes.Clone(good), '\n')}},
		"bad id":         {"k 1", map[string][]byte{"k 1": good}},
		"active missing": {"k2", map[string][]byte{"k1": good}},
		"no keys":        {"k1", nil},
	}
	for name, c := range cases {
		if _, err := NewKeyring(c.active, c.keys); err == nil {
			t.Errorf("%s: NewKeyring succeeded", name)
		}
	}
}

func TestLoadKeyring(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode checks are POSIX-only")
	}
	dir := t.TempDir()
	write := func(name string, b []byte, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil { // umask may have narrowed it
			t.Fatal(err)
		}
	}
	write("k1.key", GenerateKey(), 0o640)
	write("notes.txt", []byte("ignored"), 0o644)
	k, err := LoadKeyring(dir, "k1")
	if err != nil {
		t.Fatalf("LoadKeyring: %v", err)
	}
	if _, err := k.Seal(appA, "K", NewValueID(), []byte("v")); err != nil {
		t.Fatal(err)
	}

	write("k2.key", GenerateKey(), 0o644)
	if _, err := LoadKeyring(dir, "k1"); err == nil {
		t.Fatal("world-readable KEK file accepted")
	}
	write("k2.key", append(GenerateKey(), '\n'), 0o600)
	if _, err := LoadKeyring(dir, "k1"); err == nil {
		t.Fatal("33-byte KEK file accepted")
	}
}

func TestNewValueID(t *testing.T) {
	v4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 1000 {
		id := NewValueID()
		if !v4.MatchString(id) || seen[id] {
			t.Fatalf("NewValueID() = %q (duplicate: %v)", id, seen[id])
		}
		seen[id] = true
	}
}
