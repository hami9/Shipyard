// Package secrets implements envelope encryption for environment values and
// the immutable environment revisions built from them (ADR-0005).
//
// Each value gets a fresh 256-bit data key (DEK). The value is sealed with
// AES-256-GCM under the DEK, with AAD "app_id|key|value_id", so a ciphertext
// cannot be moved to another app, key, or row. The DEK is sealed under a key
// encryption key (KEK) with AAD "kek_id|value_id". KEKs live in files outside
// PostgreSQL, so a database dump alone reveals nothing.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// KeySize is the size of KEKs and DEKs: AES-256.
const KeySize = 32

// ErrDecrypt means a value could not be opened: wrong KEK, wrong AAD, or a
// tampered ciphertext. The causes are deliberately indistinguishable.
var ErrDecrypt = errors.New("secret value cannot be decrypted")

// kekID mirrors the CHECK on secret_values.kek_id.
var kekID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Sealed is an encrypted value as stored in secret_values.
type Sealed struct {
	ValueID    string
	Ciphertext []byte // nonce || AES-256-GCM(value) || tag
	WrappedDEK []byte // nonce || AES-256-GCM(DEK) || tag
	KEKID      string
}

// Keyring holds the KEKs. New values are sealed with the active one; any
// loaded KEK can open, which allows rotation (ADR-0005).
type Keyring struct {
	active string
	keks   map[string]cipher.AEAD
}

// NewKeyring builds a keyring from raw 32-byte keys.
func NewKeyring(active string, keys map[string][]byte) (*Keyring, error) {
	k := &Keyring{active: active, keks: make(map[string]cipher.AEAD, len(keys))}
	for id, key := range keys {
		if !kekID.MatchString(id) {
			return nil, fmt.Errorf("invalid KEK id %q", id)
		}
		aead, err := newAEAD(key)
		if err != nil {
			return nil, fmt.Errorf("KEK %s: %w", id, err)
		}
		k.keks[id] = aead
	}
	if _, ok := k.keks[active]; !ok {
		return nil, fmt.Errorf("active KEK %q is not loaded", active)
	}
	return k, nil
}

// LoadKeyring reads every <kek_id>.key file in dir. Each file holds exactly
// 32 raw bytes and must not be accessible to other users.
func LoadKeyring(dir, active string) (*Keyring, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.key"))
	if err != nil {
		return nil, err
	}
	keys := make(map[string][]byte, len(paths))
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if fi.Mode().Perm()&0o007 != 0 {
			return nil, fmt.Errorf("KEK file %s is accessible to other users (mode %v); chmod 0640", p, fi.Mode().Perm())
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		keys[strings.TrimSuffix(filepath.Base(p), ".key")] = b
	}
	return NewKeyring(active, keys)
}

// GenerateKey returns a fresh random AES-256 key, for a new KEK file.
func GenerateKey() []byte {
	k := make([]byte, KeySize)
	rand.Read(k) // never returns an error [GO-RAND]
	return k
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	// Random 96-bit nonces, prepended by Seal [GO-GCM]. Per-value DEKs seal
	// one message each; a KEK must stay below 2^32 wraps, far above any
	// single-VPS workload.
	return cipher.NewGCMWithRandomNonce(block)
}

func valueAAD(appID, key, valueID string) []byte {
	return []byte(appID + "|" + key + "|" + valueID)
}

func dekAAD(kekID, valueID string) []byte {
	return []byte(kekID + "|" + valueID)
}

// Seal encrypts plaintext for row valueID of app appID under env key.
func (k *Keyring) Seal(appID, key, valueID string, plaintext []byte) (Sealed, error) {
	dek := GenerateKey()
	defer clear(dek)
	aead, err := newAEAD(dek)
	if err != nil {
		return Sealed{}, err
	}
	return Sealed{
		ValueID:    valueID,
		Ciphertext: aead.Seal(nil, nil, plaintext, valueAAD(appID, key, valueID)),
		WrappedDEK: k.keks[k.active].Seal(nil, nil, dek, dekAAD(k.active, valueID)),
		KEKID:      k.active,
	}, nil
}

// Open decrypts a sealed value. Every failure returns ErrDecrypt.
func (k *Keyring) Open(appID, key string, s Sealed) ([]byte, error) {
	kek, ok := k.keks[s.KEKID]
	if !ok {
		return nil, fmt.Errorf("%w: KEK %q is not loaded", ErrDecrypt, s.KEKID)
	}
	dek, err := kek.Open(nil, nil, s.WrappedDEK, dekAAD(s.KEKID, s.ValueID))
	if err != nil {
		return nil, ErrDecrypt
	}
	defer clear(dek)
	aead, err := newAEAD(dek)
	if err != nil {
		return nil, ErrDecrypt
	}
	plaintext, err := aead.Open(nil, nil, s.Ciphertext, valueAAD(appID, key, s.ValueID))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// Active is the ID of the KEK that seals new values.
func (k *Keyring) Active() string { return k.active }

// Has reports whether KEK id is loaded.
func (k *Keyring) Has(id string) bool { _, ok := k.keks[id]; return ok }

// IDs lists the loaded KEKs, sorted.
func (k *Keyring) IDs() []string { return slices.Sorted(maps.Keys(k.keks)) }

// Rewrap moves a value's data key from the KEK that wrapped it to the
// active one (ADR-0012). The value's ciphertext is not touched: the DEK it
// was sealed with stays the same. Every failure returns ErrDecrypt.
func (k *Keyring) Rewrap(valueID string, wrapped []byte, kekID string) ([]byte, error) {
	kek, ok := k.keks[kekID]
	if !ok {
		return nil, fmt.Errorf("%w: KEK %q is not loaded", ErrDecrypt, kekID)
	}
	dek, err := kek.Open(nil, nil, wrapped, dekAAD(kekID, valueID))
	if err != nil {
		return nil, ErrDecrypt
	}
	defer clear(dek)
	return k.keks[k.active].Seal(nil, nil, dek, dekAAD(k.active, valueID)), nil
}

// NewValueID returns a random (version 4) UUID. The ID is part of the AAD,
// so it is chosen before sealing rather than by the database.
func NewValueID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 9562 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
