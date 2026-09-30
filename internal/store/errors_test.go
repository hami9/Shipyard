package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapError(t *testing.T) {
	other := errors.New("connection reset")
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"no rows", pgx.ErrNoRows, ErrNotFound},
		{"wrapped no rows", fmt.Errorf("scan app: %w", pgx.ErrNoRows), ErrNotFound},
		{"unique", &pgconn.PgError{Code: "23505"}, ErrConflict},
		{"check", &pgconn.PgError{Code: "23514"}, ErrInvalid},
		{"not null", &pgconn.PgError{Code: "23502"}, ErrInvalid},
		{"bad uuid", &pgconn.PgError{Code: "22P02"}, ErrInvalid},
		{"foreign key", &pgconn.PgError{Code: "23503"}, ErrReference},
		{"restrict", &pgconn.PgError{Code: "23001"}, ErrReference},
		{"immutable", &pgconn.PgError{Code: "SY001"}, ErrImmutable},
		{"unmapped state", &pgconn.PgError{Code: "40001"}, nil},
		{"non-pg error", other, other},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.in)
			switch {
			case tt.in == nil:
				if got != nil {
					t.Fatalf("mapError(nil) = %v", got)
				}
			case tt.want == nil: // passed through unchanged
				if got != tt.in {
					t.Fatalf("mapError(%v) = %v, want the input unchanged", tt.in, got)
				}
			case !errors.Is(got, tt.want):
				t.Fatalf("mapError(%v) = %v, want errors.Is %v", tt.in, got, tt.want)
			}
		})
	}
}

// PostgreSQL's message and detail quote the rejected values; they must never
// reach callers' logs.
func TestConstraintErrorOmitsValues(t *testing.T) {
	in := &pgconn.PgError{
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "apps_slug_key"`,
		Detail:         "Key (slug)=(secret-slug) already exists.",
		TableName:      "apps",
		ConstraintName: "apps_slug_key",
	}
	err := mapError(in)
	var ce *ConstraintError
	if !errors.As(err, &ce) || ce.Table != "apps" || ce.Constraint != "apps_slug_key" {
		t.Fatalf("mapError = %#v, want ConstraintError for apps_slug_key", err)
	}
	if msg := err.Error(); strings.Contains(msg, "secret-slug") || msg != "already exists: apps violates apps_slug_key" {
		t.Fatalf("Error() = %q", msg)
	}
	if errors.Is(err, in) {
		t.Fatal("ConstraintError must not wrap the original PgError")
	}
}
