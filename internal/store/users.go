package store

import (
	"context"
	"time"
)

// User is an operator account. The MVP has a single admin (ADR-0007).
type User struct {
	ID        string
	Name      string
	Role      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const userColumns = `id, name, role, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	return u, mapError(err)
}

// CreateUser adds an admin user.
func (s *Store) CreateUser(ctx context.Context, name string) (User, error) {
	return scanUser(s.q.QueryRow(ctx,
		`INSERT INTO users (name) VALUES ($1) RETURNING `+userColumns, name))
}

// UserByName returns ErrNotFound when no user has that name.
func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	return scanUser(s.q.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE name = $1`, name))
}
