package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
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

// keyDir is a KEK directory with files written at exact modes.
type keyDir struct {
	t   *testing.T
	dir string
}

func newKeyDir(t *testing.T) keyDir {
	if runtime.GOOS == "windows" {
		t.Skip("file mode checks are POSIX-only")
	}
	return keyDir{t, t.TempDir()}
}

func (d keyDir) write(name string, b []byte, mode os.FileMode) {
	d.t.Helper()
	p := filepath.Join(d.dir, name)
	if err := os.WriteFile(p, b, mode); err != nil {
		d.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		d.t.Fatal(err)
	}
}

// ADR-0012: with an HPKE KEK, the API's keyring holds only the public key.
// It seals what the worker's keyring opens, and cannot open anything.
func TestHPKEKeyring(t *testing.T) {
	d := newKeyDir(t)
	priv, pub, err := GenerateHPKE()
	if err != nil || len(priv) != 32 || len(pub) != 32 {
		t.Fatalf("GenerateHPKE = %d, %d bytes, %v", len(priv), len(pub), err)
	}
	d.write("a1.hpke", priv, 0o600)
	d.write("a1.pub", pub, 0o644)
	d.write("k1.key", GenerateKey(), 0o640)

	worker, err := LoadKeyring(d.dir, "a1")
	if err != nil || !worker.CanOpen("a1") || !worker.CanOpen("k1") || !slices.Equal(worker.IDs(), []string{"a1", "k1"}) {
		t.Fatalf("worker keyring: %v, %v", worker, err)
	}
	api, err := LoadSealKeyring(d.dir, "a1")
	if err != nil || api.CanOpen("a1") || !api.Has("a1") || api.Has("k1") {
		t.Fatalf("API keyring: %v", err)
	}
	id := NewValueID()
	s, err := api.Seal(appA, "DATABASE_URL", id, []byte("postgres://secret"))
	if err != nil || s.KEKID != "a1" {
		t.Fatalf("API seal = %+v, %v", s, err)
	}
	if _, err := api.Open(appA, "DATABASE_URL", s); !errors.Is(err, ErrDecrypt) || !strings.Contains(err.Error(), "only the public key") {
		t.Fatalf("the API opened a value: %v", err)
	}
	if got, err := worker.Open(appA, "DATABASE_URL", s); err != nil || string(got) != "postgres://secret" {
		t.Fatalf("worker open = %q, %v", got, err)
	}

	// The binding: another row, another KEK ID, or a flipped bit fails.
	other, _, _ := GenerateHPKE()
	d.write("a2.hpke", other, 0o600)
	both, _ := LoadKeyring(d.dir, "a1")
	for name, bad := range map[string]Sealed{
		"other value ID": {ValueID: NewValueID(), Ciphertext: s.Ciphertext, WrappedDEK: s.WrappedDEK, KEKID: "a1"},
		"relabelled":     {ValueID: id, Ciphertext: s.Ciphertext, WrappedDEK: s.WrappedDEK, KEKID: "a2"},
		"tampered":       {ValueID: id, Ciphertext: s.Ciphertext, WrappedDEK: flip(s.WrappedDEK), KEKID: "a1"},
	} {
		if _, err := both.Open(appA, "DATABASE_URL", bad); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: err = %v, want ErrDecrypt", name, err)
		}
	}

	// A symmetric value moves to the HPKE KEK and then needs only its key.
	symOnly, _ := LoadKeyring(d.dir, "k1")
	old, _ := symOnly.Seal(appA, "K", id, []byte("v"))
	wrapped, err := worker.Rewrap(id, old.WrappedDEK, "k1")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(d.dir, "k1.key"))
	os.Remove(filepath.Join(d.dir, "a2.hpke"))
	after, _ := LoadKeyring(d.dir, "a1")
	if got, err := after.Open(appA, "K", Sealed{ValueID: id, Ciphertext: old.Ciphertext, WrappedDEK: wrapped, KEKID: "a1"}); err != nil || string(got) != "v" {
		t.Fatalf("open after rewrap to HPKE: %q, %v", got, err)
	}
	// The API's keyring cannot rewrap: that needs opening.
	if _, err := api.Rewrap(id, wrapped, "a1"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("API rewrap: %v", err)
	}
}

func flip(b []byte) []byte { c := bytes.Clone(b); c[len(c)-1] ^= 1; return c }

func TestLoadHPKERejects(t *testing.T) {
	priv, pub, _ := GenerateHPKE()
	_, otherPub, _ := GenerateHPKE()
	for name, tc := range map[string]struct {
		files  map[string][]byte
		modes  map[string]os.FileMode
		active string
		want   string
	}{
		"group-readable private key": {map[string][]byte{"a1.hpke": priv}, map[string]os.FileMode{"a1.hpke": 0o640}, "a1", "accessible to others than its owner"},
		"mismatched public key":      {map[string][]byte{"a1.hpke": priv, "a1.pub": otherPub}, nil, "a1", "does not match its private key"},
		"defined twice":              {map[string][]byte{"a1.hpke": priv, "a1.key": GenerateKey()}, nil, "a1", "defined twice"},
		"garbage private key":        {map[string][]byte{"a1.hpke": []byte("short")}, nil, "a1", "not an X25519 HPKE private key"},
		"garbage public key":         {map[string][]byte{"k1.key": GenerateKey(), "a1.pub": []byte("short")}, nil, "k1", "not an X25519 HPKE public key"},
		"active missing":             {map[string][]byte{"a1.pub": pub}, nil, "a2", `active KEK "a2" is not loaded`},
	} {
		t.Run(name, func(t *testing.T) {
			d := newKeyDir(t)
			for f, b := range tc.files {
				mode := os.FileMode(0o600)
				if m, ok := tc.modes[f]; ok {
					mode = m
				}
				d.write(f, b, mode)
			}
			if _, err := LoadKeyring(d.dir, tc.active); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// The API's loader reads the active KEK alone and never a private key: a
// broken .hpke file or another KEK does not matter to it.
func TestLoadSealKeyring(t *testing.T) {
	d := newKeyDir(t)
	_, pub, _ := GenerateHPKE()
	d.write("a1.pub", pub, 0o644)
	d.write("a1.hpke", []byte("not even a key"), 0o644)
	d.write("k0.key", GenerateKey(), 0o644) // world-readable, but not active
	k, err := LoadSealKeyring(d.dir, "a1")
	if err != nil || !slices.Equal(k.IDs(), []string{"a1"}) {
		t.Fatalf("LoadSealKeyring(a1) = %v, %v", k, err)
	}
	d.write("k1.key", GenerateKey(), 0o640)
	if k, err := LoadSealKeyring(d.dir, "k1"); err != nil || !k.CanOpen("k1") || k.Has("a1") {
		t.Fatalf("LoadSealKeyring(k1): %v", err)
	}
	for active, want := range map[string]string{"k0": "accessible to other users", "zz": "neither .key nor .pub", "../a1": "invalid KEK id"} {
		if _, err := LoadSealKeyring(d.dir, active); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("LoadSealKeyring(%s) = %v, want %q", active, err, want)
		}
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
