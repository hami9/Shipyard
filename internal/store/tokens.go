package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Token is an API token's metadata. The plaintext is never stored: only its
// SHA-256 hash, which is also the lookup key (ADR-0007).
type Token struct {
	ID         string
	UserID     string
	Name       string
	Prefix     string // e.g. shp_AbCd1234, shown in listings and audit actors
	Scopes     []string
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// NewToken is the input to CreateToken.
type NewToken struct {
	UserID    string
	Name      string
	Prefix    string
	Hash      []byte
	Scopes    []string
	ExpiresAt *time.Time
}

const tokenColumns = `id, user_id, name, prefix, scopes, expires_at, last_used_at, revoked_at, created_at`

func scanToken(row interface{ Scan(...any) error }) (Token, error) {
	var t Token
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.Scopes, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt)
	return t, mapError(err)
}

// CreateToken stores a token's hash and metadata.
func (s *Store) CreateToken(ctx context.Context, n NewToken) (Token, error) {
	return scanToken(s.q.QueryRow(ctx, `
		INSERT INTO api_tokens (user_id, name, prefix, sha256_hash, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+tokenColumns,
		n.UserID, n.Name, n.Prefix, n.Hash, n.Scopes, n.ExpiresAt))
}

// ActiveTokenByHash returns the token with this hash if it is neither revoked
// nor expired, and ErrNotFound otherwise. Callers cannot tell the cases apart,
// so the API cannot leak which one applied.
func (s *Store) ActiveTokenByHash(ctx context.Context, hash []byte) (Token, error) {
	return scanToken(s.q.QueryRow(ctx, `
		SELECT `+tokenColumns+` FROM api_tokens
		WHERE sha256_hash = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`, hash))
}

// TouchToken records a use. It writes at most once a minute per token, so
// authenticating a busy client does not turn every request into a write.
func (s *Store) TouchToken(ctx context.Context, id string) error {
	_, err := s.q.Exec(ctx, `
		UPDATE api_tokens SET last_used_at = now()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, id)
	return mapError(err)
}

// ListTokens returns every token, newest first.
func (s *Store) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.q.Query(ctx, `SELECT `+tokenColumns+` FROM api_tokens ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, mapError(err)
	}
	tokens, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Token, error) { return scanToken(r) })
	return tokens, mapError(err)
}

// RevokeToken revokes the token with this prefix. Revoking twice is a no-op
// that returns the original revocation; an unknown prefix returns ErrNotFound.
func (s *Store) RevokeToken(ctx context.Context, prefix string) (Token, error) {
	return scanToken(s.q.QueryRow(ctx, `
		UPDATE api_tokens SET revoked_at = coalesce(revoked_at, now())
		WHERE prefix = $1 RETURNING `+tokenColumns, prefix))
}
