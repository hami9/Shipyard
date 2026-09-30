package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// OperationStore is what the deploy and operation endpoints need.
type OperationStore interface {
	EnqueueOperation(ctx context.Context, n store.NewOperation) (store.Enqueued, error)
	OperationByID(ctx context.Context, id string) (store.Operation, error)
	OperationEvents(ctx context.Context, opID string, afterSeq int64, limit int) ([]store.OperationEvent, error)
	Releases(ctx context.Context, appID, before string, limit int) ([]store.Release, error)
	// For rollbacks: the target, and its revision against the latest.
	DeploymentByID(ctx context.Context, id string) (store.Deployment, error)
	EnvRevisionByID(ctx context.Context, id string) (store.EnvRevision, error)
	LatestEnvRevision(ctx context.Context, appID string) (store.EnvRevision, error)
}

// Client keys are namespaced so they can never collide with the webhook
// receiver's "gh:<delivery>" keys (ARCHITECTURE §5, step 1).
const (
	headerIdempotencyKey = "Idempotency-Key"
	clientKeyPrefix      = "api:"
)

type operationJSON struct {
	ID             string          `json:"id"`
	AppID          string          `json:"app_id"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"`
	Phase          string          `json:"phase,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey string          `json:"idempotency_key"`
	Attempt        int             `json:"attempt"`
	MaxAttempts    int             `json:"max_attempts"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

func toOperationJSON(o store.Operation) operationJSON {
	return operationJSON{o.ID, o.AppID, o.Kind, o.Status, o.Phase, o.Payload, o.IdempotencyKey,
		o.Attempt, o.MaxAttempts, o.LastError, o.CreatedAt, o.FinishedAt}
}

type opHandlers struct {
	*appHandlers
	ops             OperationStore
	poll, keepalive time.Duration // event streams
}

// deploy admits a deploy request. It answers 202 for a new operation and
// 200 with the original operation for a repeated Idempotency-Key. The worker
// does the rest; the API never touches Docker or git (invariant 1).
func (h *opHandlers) deploy(w http.ResponseWriter, r *http.Request) {
	var req app.DeployPayload
	if r.ContentLength != 0 {
		var body struct {
			Ref *string `json:"ref"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, r, h.log, err)
			return
		}
		if body.Ref != nil {
			req.Ref = *body.Ref
		}
	}
	var fe app.FieldErrors
	if req.Ref != "" {
		if err := app.CheckCommitSHA(req.Ref); err != nil {
			fe = append(fe, app.FieldError{Field: "ref", Detail: err.Error()})
		}
	}
	key, fe := idempotencyKey(r, fe)
	if len(fe) > 0 {
		writeError(w, r, h.log, fe)
		return
	}
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	payload, _ := json.Marshal(req)
	h.admit(w, r, a, app.KindDeploy, key, payload)
}

// idempotencyKey reads the client's Idempotency-Key, or makes one up.
func idempotencyKey(r *http.Request, fe app.FieldErrors) (string, app.FieldErrors) {
	key := r.Header.Get(headerIdempotencyKey)
	if key == "" {
		return "auto:" + rand.Text(), fe
	}
	if err := app.CheckIdempotencyKey(key); err != nil {
		fe = append(fe, app.FieldError{Field: headerIdempotencyKey, Detail: err.Error()})
	}
	return key, fe
}

// admit queues an operation of app and answers like deploy.
func (h *opHandlers) admit(w http.ResponseWriter, r *http.Request, a store.App, kind, key string, payload []byte) {
	res, err := h.ops.EnqueueOperation(r.Context(), store.NewOperation{
		AppID: a.ID, Kind: kind, IdempotencyKey: clientKeyPrefix + key, Payload: payload,
	})
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	// A replay must be the same request, not just the same key.
	if !res.Created && !samePayload(res.Operation.Payload, payload) {
		writeError(w, r, h.log, store.ErrIdempotencyMismatch)
		return
	}
	status := http.StatusAccepted
	if !res.Created {
		status = http.StatusOK
	}
	w.Header().Set("Location", "/v1/operations/"+res.Operation.ID)
	writeJSON(w, status, "application/json", struct {
		Operation  operationJSON `json:"operation"`
		Created    bool          `json:"created"`
		Superseded []string      `json:"superseded"`
	}{toOperationJSON(res.Operation), res.Created, orEmpty(res.Superseded)})
}

func (h *opHandlers) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		writeError(w, r, h.log, store.ErrNotFound)
		return
	}
	op, err := h.ops.OperationByID(r.Context(), id)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	writeJSON(w, http.StatusOK, "application/json", toOperationJSON(op))
}

// samePayload compares JSON objects semantically; jsonb reformats them.
func samePayload(stored, requested []byte) bool {
	var a, b any
	if json.Unmarshal(stored, &a) != nil || json.Unmarshal(requested, &b) != nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
