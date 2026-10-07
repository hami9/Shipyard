// Package secrets implements envelope encryption for environment values and
// the immutable environment revisions built from them (ADR-0005).
//
// Each value gets a fresh 256-bit data key (DEK). The value is sealed with
// AES-256-GCM under the DEK, with AAD "app_id|key|value_id", so a ciphertext
// cannot be moved to another app, key, or row. The DEK is wrapped by a key
// encryption key (KEK), bound to "kek_id|value_id". KEKs live in files
// outside PostgreSQL, so a database dump alone reveals nothing.
//
// A KEK is either symmetric (AES-256-GCM, <id>.key) or an HPKE key pair
// (<id>.hpke private, <id>.pub public; ADR-0012). With an HPKE KEK active,
// the API needs only the public key: it can seal values but never open one.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// KeySize is the size of symmetric KEKs and DEKs: AES-256.
const KeySize = 32

// KEK file suffixes (ADR-0012).
const (
	SymmetricSuffix = ".key"  // 32 raw bytes, shared by the API and worker
	PrivateSuffix   = ".hpke" // HPKE private key, the worker's only
	PublicSuffix    = ".pub"  // HPKE public key, all the API needs
)

// ErrDecrypt means a value could not be opened: wrong KEK, wrong AAD, or a
// tampered ciphertext. The causes are deliberately indistinguishable.
var ErrDecrypt = errors.New("secret value cannot be decrypted")

// kekID mirrors the CHECK on secret_values.kek_id.
var kekID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// The HPKE suite: all three parts are final in RFC 9180 [GO-HPKE][RFC9180].
// X-Wing (post-quantum) is still a draft; a later rotation can move to it.
var (
	hpkeKEM  = hpke.DHKEM(ecdh.X25519())
	hpkeKDF  = hpke.HKDFSHA256()
	hpkeAEAD = hpke.AES256GCM()
)

// Sealed is an encrypted value as stored in secret_values.
type Sealed struct {
	ValueID    string
	Ciphertext []byte // nonce || AES-256-GCM(value) || tag
	WrappedDEK []byte // the DEK wrapped by KEKID (AES-256-GCM or HPKE)
	KEKID      string
}

// kek wraps and unwraps data keys, bound to the KEK's and value's IDs.
type kek interface {
	wrap(dek []byte, kekID, valueID string) ([]byte, error)
	unwrap(wrapped []byte, kekID, valueID string) ([]byte, error)
	canOpen() bool
}

type symmetricKEK struct{ aead cipher.AEAD }

func (s symmetricKEK) wrap(dek []byte, id, valueID string) ([]byte, error) {
	return s.aead.Seal(nil, nil, dek, dekAAD(id, valueID)), nil
}

func (s symmetricKEK) unwrap(wrapped []byte, id, valueID string) ([]byte, error) {
	return s.aead.Open(nil, nil, wrapped, dekAAD(id, valueID))
}

func (symmetricKEK) canOpen() bool { return true }

// hpkeKEK seals to pub; it opens only when the private key is loaded.
type hpkeKEK struct {
	pub  hpke.PublicKey
	priv hpke.PrivateKey // nil: seal only (the API)
}

func (h hpkeKEK) wrap(dek []byte, id, valueID string) ([]byte, error) {
	return hpke.Seal(h.pub, hpkeKDF, hpkeAEAD, hpkeInfo(id, valueID), dek)
}

func (h hpkeKEK) unwrap(wrapped []byte, id, valueID string) ([]byte, error) {
	if h.priv == nil {
		return nil, errors.New("only the public key is loaded")
	}
	return hpke.Open(h.priv, hpkeKDF, hpkeAEAD, hpkeInfo(id, valueID), wrapped)
}

func (h hpkeKEK) canOpen() bool { return h.priv != nil }

// Keyring holds the KEKs. New values are sealed with the active one; any
// loaded KEK that has its secret part can open, which allows rotation.
type Keyring struct {
	active string
	keks   map[string]kek
}

// NewKeyring builds a keyring from raw 32-byte symmetric keys.
func NewKeyring(active string, keys map[string][]byte) (*Keyring, error) {
	k := &Keyring{active: active, keks: make(map[string]kek, len(keys))}
	for id, key := range keys {
		if err := k.addSymmetric(id, key); err != nil {
			return nil, err
		}
	}
	return k, k.checkActive()
}

func (k *Keyring) checkActive() error {
	if _, ok := k.keks[k.active]; !ok {
		return fmt.Errorf("active KEK %q is not loaded", k.active)
	}
	return nil
}

func (k *Keyring) add(id string, kk kek) error {
	if !kekID.MatchString(id) {
		return fmt.Errorf("invalid KEK id %q", id)
	}
	if _, dup := k.keks[id]; dup {
		return fmt.Errorf("KEK %s is defined twice (a %s and an HPKE key)", id, SymmetricSuffix)
	}
	k.keks[id] = kk
	return nil
}

func (k *Keyring) addSymmetric(id string, key []byte) error {
	aead, err := newAEAD(key)
	if err != nil {
		return fmt.Errorf("KEK %s: %w", id, err)
	}
	return k.add(id, symmetricKEK{aead})
}

func (k *Keyring) addPrivate(id string, b []byte) error {
	priv, err := hpkeKEM.NewPrivateKey(b)
	if err != nil {
		return fmt.Errorf("KEK %s: not an X25519 HPKE private key: %w", id, err)
	}
	return k.add(id, hpkeKEK{pub: priv.PublicKey(), priv: priv})
}

func (k *Keyring) addPublic(id string, b []byte) error {
	pub, err := hpkeKEM.NewPublicKey(b)
	if err != nil {
		return fmt.Errorf("KEK %s: not an X25519 HPKE public key: %w", id, err)
	}
	return k.add(id, hpkeKEK{pub: pub})
}

// LoadKeyring reads every KEK in dir, for the worker: <id>.key files (32
// raw bytes, not accessible to other users), <id>.hpke private keys
// (readable by their owner only), and <id>.pub public keys, which must
// match their private key when both exist.
func LoadKeyring(dir, active string) (*Keyring, error) {
	k := &Keyring{active: active, keks: map[string]kek{}}
	pubs := map[string][]byte{}
	for _, suffix := range []string{SymmetricSuffix, PrivateSuffix, PublicSuffix} {
		paths, err := filepath.Glob(filepath.Join(dir, "*"+suffix))
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			id := strings.TrimSuffix(filepath.Base(p), suffix)
			b, err := readKEKFile(p, suffix)
			if err != nil {
				return nil, err
			}
			switch suffix {
			case SymmetricSuffix:
				err = k.addSymmetric(id, b)
			case PrivateSuffix:
				err = k.addPrivate(id, b)
			case PublicSuffix:
				pubs[id] = b
			}
			if err != nil {
				return nil, err
			}
		}
	}
	for id, b := range pubs {
		if h, ok := k.keks[id].(hpkeKEK); ok {
			if string(h.pub.Bytes()) != string(b) {
				return nil, fmt.Errorf("KEK %s: %s%s does not match its private key", id, id, PublicSuffix)
			}
			continue
		}
		if err := k.addPublic(id, b); err != nil {
			return nil, err
		}
	}
	return k, k.checkActive()
}

// LoadSealKeyring reads only the active KEK, for the API, which seals and
// never opens: <active>.key if it exists, else <active>.pub. It never reads
// a private key.
func LoadSealKeyring(dir, active string) (*Keyring, error) {
	if !kekID.MatchString(active) {
		return nil, fmt.Errorf("invalid KEK id %q", active)
	}
	k := &Keyring{active: active, keks: map[string]kek{}}
	path := filepath.Join(dir, active+SymmetricSuffix)
	b, err := readKEKFile(path, SymmetricSuffix)
	if err == nil {
		return k, k.addSymmetric(active, b)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	path = filepath.Join(dir, active+PublicSuffix)
	if b, err = readKEKFile(path, PublicSuffix); err != nil {
		return nil, fmt.Errorf("active KEK %q: neither %s nor %s in %s: %w", active, SymmetricSuffix, PublicSuffix, dir, err)
	}
	return k, k.addPublic(active, b)
}

// readKEKFile refuses key files that other users can read: a symmetric key
// must not be world-accessible (the API and worker share its group), and a
// private key must be its owner's alone.
func readKEKFile(path, suffix string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	switch perm := fi.Mode().Perm(); {
	case suffix == SymmetricSuffix && perm&0o007 != 0:
		return nil, fmt.Errorf("KEK file %s is accessible to other users (mode %v); chmod 0640", path, perm)
	case suffix == PrivateSuffix && perm&0o077 != 0:
		return nil, fmt.Errorf("private KEK file %s is accessible to others than its owner (mode %v); chmod 0600", path, perm)
	}
	return os.ReadFile(path)
}

// GenerateKey returns a fresh random AES-256 key, for a new KEK file.
func GenerateKey() []byte {
	k := make([]byte, KeySize)
	rand.Read(k) // never returns an error [GO-RAND]
	return k
}

// GenerateHPKE returns a new HPKE key pair for an asymmetric KEK, as the
// contents of its <id>.hpke and <id>.pub files.
func GenerateHPKE() (private, public []byte, err error) {
	priv, err := hpkeKEM.GenerateKey()
	if err != nil {
		return nil, nil, err
	}
	if private, err = priv.Bytes(); err != nil {
		return nil, nil, err
	}
	return private, priv.PublicKey().Bytes(), nil
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

// hpkeInfo binds an HPKE-wrapped DEK to its KEK and row, as dekAAD does for
// symmetric KEKs; the version prefix keeps the context distinct.
func hpkeInfo(kekID, valueID string) []byte {
	return []byte("shipyard-dek-v1|" + kekID + "|" + valueID)
}

// Seal encrypts plaintext for row valueID of app appID under env key.
func (k *Keyring) Seal(appID, key, valueID string, plaintext []byte) (Sealed, error) {
	dek := GenerateKey()
	defer clear(dek)
	aead, err := newAEAD(dek)
	if err != nil {
		return Sealed{}, err
	}
	wrapped, err := k.keks[k.active].wrap(dek, k.active, valueID)
	if err != nil {
		return Sealed{}, fmt.Errorf("wrap with KEK %s: %w", k.active, err)
	}
	return Sealed{
		ValueID:    valueID,
		Ciphertext: aead.Seal(nil, nil, plaintext, valueAAD(appID, key, valueID)),
		WrappedDEK: wrapped,
		KEKID:      k.active,
	}, nil
}

// openDEK unwraps a value's data key.
func (k *Keyring) openDEK(valueID string, wrapped []byte, id string) ([]byte, error) {
	kk, ok := k.keks[id]
	if !ok {
		return nil, fmt.Errorf("%w: KEK %q is not loaded", ErrDecrypt, id)
	}
	if !kk.canOpen() {
		return nil, fmt.Errorf("%w: only the public key of KEK %q is loaded", ErrDecrypt, id)
	}
	dek, err := kk.unwrap(wrapped, id, valueID)
	if err != nil {
		return nil, ErrDecrypt
	}
	return dek, nil
}

// Open decrypts a sealed value. Every failure returns ErrDecrypt.
func (k *Keyring) Open(appID, key string, s Sealed) ([]byte, error) {
	dek, err := k.openDEK(s.ValueID, s.WrappedDEK, s.KEKID)
	if err != nil {
		return nil, err
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

// Has reports whether KEK id is loaded, if only its public key.
func (k *Keyring) Has(id string) bool { _, ok := k.keks[id]; return ok }

// CanOpen reports whether KEK id is loaded with what opens data keys: a
// symmetric key, or an HPKE private key.
func (k *Keyring) CanOpen(id string) bool { kk, ok := k.keks[id]; return ok && kk.canOpen() }

// IDs lists the loaded KEKs, sorted.
func (k *Keyring) IDs() []string { return slices.Sorted(maps.Keys(k.keks)) }

// Rewrap moves a value's data key from the KEK that wrapped it to the
// active one (ADR-0012). The value's ciphertext is not touched: the DEK it
// was sealed with stays the same. Every failure to open returns ErrDecrypt.
func (k *Keyring) Rewrap(valueID string, wrapped []byte, kekID string) ([]byte, error) {
	dek, err := k.openDEK(valueID, wrapped, kekID)
	if err != nil {
		return nil, err
	}
	defer clear(dek)
	return k.keks[k.active].wrap(dek, k.active, valueID)
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
