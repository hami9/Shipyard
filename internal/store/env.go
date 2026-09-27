package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// SecretValue is one encrypted, immutable environment value (ADR-0005).
type SecretValue struct {
	ID         string
	AppID      string
	Key        string
	Ciphertext []byte
	WrappedDEK []byte
	KEKID      string
	CreatedAt  time.Time
}

// EnvEntry is one key of a revision: exactly one of SecretValueID and
// PlainValue is set.
type EnvEntry struct {
	Key           string
	SecretValueID *string
	PlainValue    *string
}

// EnvRevision is an immutable, numbered snapshot of an app's environment.
type EnvRevision struct {
	ID        string
	AppID     string
	Number    int
	CreatedAt time.Time
	Entries   []EnvEntry // sorted by key
}

// LockApp takes a row lock on the app until the transaction ends, which
// serializes writers such as revision creation. Call it inside InTx.
func (s *Store) LockApp(ctx context.Context, appID string) error {
	var one int
	return mapError(s.q.QueryRow(ctx, `SELECT 1 FROM apps WHERE id = $1 FOR UPDATE`, appID).Scan(&one))
}

// InsertSecretValue stores a sealed value under the ID used in its AAD.
func (s *Store) InsertSecretValue(ctx context.Context, v SecretValue) (SecretValue, error) {
	err := s.q.QueryRow(ctx, `
		INSERT INTO secret_values (id, app_id, key, ciphertext, wrapped_dek, kek_id)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
		v.ID, v.AppID, v.Key, v.Ciphertext, v.WrappedDEK, v.KEKID).Scan(&v.CreatedAt)
	return v, mapError(err)
}

// SecretValuesByID returns the requested rows keyed by ID. Missing IDs are
// simply absent.
func (s *Store) SecretValuesByID(ctx context.Context, ids []string) (map[string]SecretValue, error) {
	rows, err := s.q.Query(ctx, `
		SELECT id, app_id, key, ciphertext, wrapped_dek, kek_id, created_at
		FROM secret_values WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, mapError(err)
	}
	out := make(map[string]SecretValue, len(ids))
	var v SecretValue
	_, err = pgx.ForEachRow(rows, []any{&v.ID, &v.AppID, &v.Key, &v.Ciphertext, &v.WrappedDEK, &v.KEKID, &v.CreatedAt}, func() error {
		out[v.ID] = v
		v = SecretValue{} // fresh slices per row, so entries never share buffers
		return nil
	})
	return out, mapError(err)
}

// CreateEnvRevision stores the next revision for an app with these entries.
// Call it inside InTx after LockApp, so concurrent writers get consecutive
// numbers instead of a unique violation.
func (s *Store) CreateEnvRevision(ctx context.Context, appID string, entries []EnvEntry) (EnvRevision, error) {
	r := EnvRevision{AppID: appID}
	err := s.q.QueryRow(ctx, `
		INSERT INTO env_revisions (app_id, number)
		SELECT $1, coalesce(max(number), 0) + 1 FROM env_revisions WHERE app_id = $1
		RETURNING id, number, created_at`, appID).Scan(&r.ID, &r.Number, &r.CreatedAt)
	if err != nil {
		return EnvRevision{}, mapError(err)
	}
	for _, e := range entries {
		if _, err := s.q.Exec(ctx, `
			INSERT INTO env_revision_entries (revision_id, app_id, key, secret_value_id, plain_value)
			VALUES ($1, $2, $3, $4, $5)`, r.ID, appID, e.Key, e.SecretValueID, e.PlainValue); err != nil {
			return EnvRevision{}, mapError(err)
		}
	}
	return s.EnvRevisionByID(ctx, r.ID)
}

// LatestEnvRevision returns the app's highest-numbered revision, or
// ErrNotFound if the app has none.
func (s *Store) LatestEnvRevision(ctx context.Context, appID string) (EnvRevision, error) {
	var id string
	err := s.q.QueryRow(ctx, `
		SELECT id FROM env_revisions WHERE app_id = $1 ORDER BY number DESC LIMIT 1`, appID).Scan(&id)
	if err != nil {
		return EnvRevision{}, mapError(err)
	}
	return s.EnvRevisionByID(ctx, id)
}

// EnvRevisionByID returns a revision with its entries.
func (s *Store) EnvRevisionByID(ctx context.Context, id string) (EnvRevision, error) {
	var r EnvRevision
	err := s.q.QueryRow(ctx, `SELECT id, app_id, number, created_at FROM env_revisions WHERE id = $1`, id).
		Scan(&r.ID, &r.AppID, &r.Number, &r.CreatedAt)
	if err != nil {
		return EnvRevision{}, mapError(err)
	}
	rows, err := s.q.Query(ctx, `
		SELECT key, secret_value_id, plain_value FROM env_revision_entries
		WHERE revision_id = $1 ORDER BY key`, id)
	if err != nil {
		return EnvRevision{}, mapError(err)
	}
	var e EnvEntry
	_, err = pgx.ForEachRow(rows, []any{&e.Key, &e.SecretValueID, &e.PlainValue}, func() error {
		r.Entries = append(r.Entries, e)
		e = EnvEntry{}
		return nil
	})
	return r, mapError(err)
}
