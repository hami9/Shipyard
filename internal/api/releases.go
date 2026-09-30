package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// Release history page sizes (P3.1).
const (
	defaultReleaseLimit = 20
	maxReleaseLimit     = 100
)

type releaseJSON struct {
	ID            string     `json:"id"`
	OperationID   string     `json:"operation_id"`
	Kind          string     `json:"kind"`
	RollbackOf    *string    `json:"rollback_of,omitempty"` // a rollback's source deployment
	Status        string     `json:"status"`
	Commit        string     `json:"commit"`
	ImageID       string     `json:"image_id,omitempty"`
	EnvRevision   int        `json:"env_revision"` // 0: no environment
	FailureReason string     `json:"failure_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ActiveAt      *time.Time `json:"active_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
}

// releases lists an app's deployments, newest first. A page holds limit
// deployments; "next" is the cursor for the following page (?before=). It
// is absent when a page comes back short, so a full last page costs one
// more, empty request. The rows are the database's: which images are
// still on the host is the worker's knowledge (invariant 1).
func (h *opHandlers) releases(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var fe app.FieldErrors
	limit := defaultReleaseLimit
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxReleaseLimit {
			fe = append(fe, app.FieldError{Field: "limit", Detail: "must be a number from 1 to " + strconv.Itoa(maxReleaseLimit)})
		}
		limit = n
	}
	before := q.Get("before")
	if before != "" && !uuidRE.MatchString(before) {
		fe = append(fe, app.FieldError{Field: "before", Detail: "must be a deployment ID"})
	}
	if len(fe) > 0 {
		writeError(w, r, h.log, fe)
		return
	}
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	rels, err := h.ops.Releases(r.Context(), a.ID, before, limit)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, h.log, app.FieldErrors{{Field: "before", Detail: "is not a deployment of this app"}})
		return
	}
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	out := struct {
		Deployments []releaseJSON `json:"deployments"`
		Next        string        `json:"next,omitempty"`
	}{Deployments: make([]releaseJSON, len(rels))}
	for i, d := range rels {
		out.Deployments[i] = releaseJSON{d.ID, d.OperationID, d.Kind, d.SourceDeployment, d.Status, d.SourceCommitSHA, d.ImageID,
			d.EnvRevision, d.FailureReason, d.CreatedAt, d.ActiveAt, d.EndedAt}
	}
	if len(rels) == limit {
		out.Next = rels[len(rels)-1].ID
	}
	writeJSON(w, http.StatusOK, "application/json", out)
}
