package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Domain errors. Callers test them with errors.Is; the API maps them to
// problem+json in one place (CLAUDE.md §5).
var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("already exists")
	ErrInvalid   = errors.New("invalid value")
	ErrReference = errors.New("referenced row missing or still in use")
	ErrImmutable = errors.New("row is immutable")
)

// ConstraintError says which database rule rejected a write. It deliberately
// drops PostgreSQL's message and detail, which quote the offending values
// ("Key (slug)=(web) already exists"), so it is safe to log.
type ConstraintError struct {
	Kind       error // one of the Err* values above
	Table      string
	Constraint string // empty for NOT NULL and type errors
	Column     string // set by PostgreSQL for NOT NULL violations
}

func (e *ConstraintError) Error() string {
	switch {
	case e.Constraint != "":
		return fmt.Sprintf("%v: %s violates %s", e.Kind, e.Table, e.Constraint)
	case e.Column != "":
		return fmt.Sprintf("%v: %s.%s", e.Kind, e.Table, e.Column)
	default:
		return e.Kind.Error()
	}
}

func (e *ConstraintError) Unwrap() error { return e.Kind }

// SQLSTATE classes the schema raises (migrations/0002_schema_v1.sql).
var stateKinds = map[string]error{
	"23505": ErrConflict,  // unique_violation
	"23514": ErrInvalid,   // check_violation
	"23502": ErrInvalid,   // not_null_violation
	"22P02": ErrInvalid,   // invalid_text_representation, e.g. a malformed uuid
	"22001": ErrInvalid,   // string_data_right_truncation
	"22003": ErrInvalid,   // numeric_value_out_of_range
	"23503": ErrReference, // foreign_key_violation
	"23001": ErrReference, // restrict_violation (ON DELETE RESTRICT)
	"SY001": ErrImmutable, // shipyard_reject_change()
}

// mapError turns pgx errors into domain errors and passes others through.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	kind, ok := stateKinds[pgErr.Code]
	if !ok {
		return err
	}
	return &ConstraintError{Kind: kind, Table: pgErr.TableName, Constraint: pgErr.ConstraintName, Column: pgErr.ColumnName}
}
