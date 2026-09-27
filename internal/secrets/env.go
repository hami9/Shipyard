package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/hami9/shipyard/internal/store"
)

// MaxValueSize bounds one environment value. Container environments are
// passed on the process command line, so huge values are a mistake.
const MaxValueSize = 64 << 10

var (
	// ErrInvalidValue is a value that cannot be an environment variable.
	ErrInvalidValue = errors.New("invalid environment value")
	// ErrUnknownKey is an Unset of a key the latest revision does not have.
	ErrUnknownKey = errors.New("environment key not set")
)

// Env manages an app's environment revisions. The API uses Set, Unset, and
// Keys, which never decrypt; only the worker calls Resolve (ADR-0005).
type Env struct {
	keys  *Keyring
	store *store.Store
}

// NewEnv returns an Env that seals new values with keys.
func NewEnv(keys *Keyring, s *store.Store) *Env {
	return &Env{keys: keys, store: s}
}

// Var describes one key of a revision without its value.
type Var struct {
	Key    string
	Secret bool
}

// Set creates a revision in which key has value. A secret value is sealed
// into a new row; every other entry is copied by reference, without
// decrypting it.
func (e *Env) Set(ctx context.Context, appID, key string, value []byte, secret bool) (store.EnvRevision, error) {
	if len(value) > MaxValueSize || bytes.IndexByte(value, 0) >= 0 {
		return store.EnvRevision{}, fmt.Errorf("%w: at most %d bytes and no NUL", ErrInvalidValue, MaxValueSize)
	}
	entry := store.EnvEntry{Key: key}
	var rev store.EnvRevision
	err := e.store.InTx(ctx, func(tx *store.Store) error {
		entries, err := lockedEntries(ctx, tx, appID)
		if err != nil {
			return err
		}
		if secret {
			id := NewValueID()
			sealed, err := e.keys.Seal(appID, key, id, value)
			if err != nil {
				return err
			}
			if _, err := tx.InsertSecretValue(ctx, store.SecretValue{ID: id, AppID: appID, Key: key,
				Ciphertext: sealed.Ciphertext, WrappedDEK: sealed.WrappedDEK, KEKID: sealed.KEKID}); err != nil {
				return fmt.Errorf("store secret %s: %w", key, err)
			}
			entry.SecretValueID = &id
		} else {
			plain := string(value)
			entry.PlainValue = &plain
		}
		rev, err = tx.CreateEnvRevision(ctx, appID, append(without(entries, key), entry))
		return err
	})
	return rev, err
}

// Unset creates a revision without key.
func (e *Env) Unset(ctx context.Context, appID, key string) (store.EnvRevision, error) {
	var rev store.EnvRevision
	err := e.store.InTx(ctx, func(tx *store.Store) error {
		entries, err := lockedEntries(ctx, tx, appID)
		if err != nil {
			return err
		}
		kept := without(entries, key)
		if len(kept) == len(entries) {
			return fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		rev, err = tx.CreateEnvRevision(ctx, appID, kept)
		return err
	})
	return rev, err
}

// Keys lists the latest revision's keys. It returns revision 0 and no keys
// when the app has no environment yet.
func (e *Env) Keys(ctx context.Context, appID string) (int, []Var, error) {
	rev, err := e.store.LatestEnvRevision(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		if _, err := e.store.AppByID(ctx, appID); err != nil {
			return 0, nil, err
		}
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	vars := make([]Var, len(rev.Entries))
	for i, en := range rev.Entries {
		vars[i] = Var{Key: en.Key, Secret: en.SecretValueID != nil}
	}
	return rev.Number, vars, nil
}

// Resolve decrypts a revision into KEY -> value, for starting a container.
// Callers must never log the result.
func (e *Env) Resolve(ctx context.Context, revisionID string) (map[string]string, error) {
	rev, err := e.store.EnvRevisionByID(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, en := range rev.Entries {
		if en.SecretValueID != nil {
			ids = append(ids, *en.SecretValueID)
		}
	}
	values, err := e.store.SecretValuesByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(rev.Entries))
	for _, en := range rev.Entries {
		if en.PlainValue != nil {
			env[en.Key] = *en.PlainValue
			continue
		}
		v, ok := values[*en.SecretValueID]
		if !ok {
			return nil, fmt.Errorf("revision %d: secret for %s is missing", rev.Number, en.Key)
		}
		plain, err := e.keys.Open(rev.AppID, en.Key, Sealed{ValueID: v.ID, Ciphertext: v.Ciphertext, WrappedDEK: v.WrappedDEK, KEKID: v.KEKID})
		if err != nil {
			return nil, fmt.Errorf("revision %d, key %s: %w", rev.Number, en.Key, err)
		}
		env[en.Key] = string(plain)
	}
	return env, nil
}

// lockedEntries locks the app and returns its latest entries (none for a
// first revision).
func lockedEntries(ctx context.Context, tx *store.Store, appID string) ([]store.EnvEntry, error) {
	if err := tx.LockApp(ctx, appID); err != nil {
		return nil, fmt.Errorf("app %s: %w", appID, err)
	}
	latest, err := tx.LatestEnvRevision(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return latest.Entries, err
}

func without(entries []store.EnvEntry, key string) []store.EnvEntry {
	out := make([]store.EnvEntry, 0, len(entries))
	for _, en := range entries {
		if en.Key != key {
			out = append(out, en)
		}
	}
	return out
}
