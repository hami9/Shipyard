// Package store is Shipyard's PostgreSQL persistence layer. PostgreSQL is the
// source of truth for desired state, operations, and history (ADR-0002).
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pingTimeout = 5 * time.Second

// querier is what pgxpool.Pool and pgx.Tx have in common, so every query
// method runs the same way inside or outside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store runs Shipyard's queries. The zero value is not usable; call New.
type Store struct {
	pool *pgxpool.Pool // nil when the Store is bound to a transaction
	q    querier
}

// New returns a Store that runs each call in its own implicit transaction.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: pool}
}

// InTx runs fn with a Store bound to one transaction. It commits when fn
// returns nil and rolls back otherwise. Inside a transaction, InTx joins it
// instead of nesting, so use cases can compose.
func (s *Store) InTx(ctx context.Context, fn func(tx *Store) error) error {
	if s.pool == nil {
		return fn(s)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(&Store{q: tx})
	})
}

// Open connects to PostgreSQL and verifies the connection with a ping, so a
// misconfigured process fails at startup instead of on its first request.
//
// Errors never contain the password: pgx redacts it when it reports parse
// errors, and TestOpenRedactsPassword guards that behavior.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
