package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// badRequest is a malformed request: bad JSON, wrong content type, too large.
type badRequest struct {
	status int
	detail string
}

func (b badRequest) Error() string { return b.detail }

// friendly explains constraint violations that clients can cause.
var friendly = map[string]string{
	"apps_slug_key": "an app with this slug already exists",
}

// writeError maps an error to problem+json. It is the only place that does,
// so every handler reports errors the same way (CLAUDE.md §5). Unexpected
// errors are logged and hidden behind a generic 500.
func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var (
		fe  app.FieldErrors
		br  badRequest
		ce  *store.ConstraintError
		msg string
	)
	if errors.As(err, &ce) {
		msg = friendly[ce.Constraint]
	}
	switch {
	case errors.As(err, &fe):
		writeJSON(w, http.StatusUnprocessableEntity, "application/problem+json", Problem{
			Type: "about:blank", Title: http.StatusText(http.StatusUnprocessableEntity),
			Status: http.StatusUnprocessableEntity, Detail: "some fields are invalid", Errors: fe,
		})
	case errors.As(err, &br):
		writeProblem(w, br.status, br.detail)
	case errors.Is(err, store.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrAppBusy):
		writeProblem(w, http.StatusConflict, store.ErrAppBusy.Error())
	case errors.Is(err, store.ErrConflict):
		writeProblem(w, http.StatusConflict, orDefault(msg, "the request conflicts with existing data"))
	case errors.Is(err, store.ErrInvalid):
		// The Go validation should have caught it; the database is the last line.
		writeProblem(w, http.StatusUnprocessableEntity, orDefault(msg, "a value is out of range"))
	case errors.Is(err, store.ErrReference), errors.Is(err, store.ErrImmutable):
		writeProblem(w, http.StatusConflict, orDefault(msg, "the request conflicts with existing data"))
	default:
		log.ErrorContext(r.Context(), "request failed", slog.String("route", r.Pattern), slog.Any("err", err))
		writeProblem(w, http.StatusInternalServerError, "internal error")
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
