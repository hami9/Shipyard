package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/hami9/shipyard/internal/webhook"
)

// GitHub expects an answer within 10 s and marks a slower delivery failed
// [GH-BP]. The body may take up to webhookReadTimeout to arrive and the sink
// up to webhookSinkTimeout to record it, which leaves headroom.
const (
	webhookReadTimeout = 5 * time.Second
	webhookSinkTimeout = 3 * time.Second
)

// Outcomes of a verified delivery, as the receiver answers them.
const (
	OutcomePong    = "pong"
	OutcomeQueued  = "queued"
	OutcomeIgnored = "ignored"
)

// PushOutcome is what the sink did with a verified push.
type PushOutcome struct {
	Outcome     string // OutcomeQueued or OutcomeIgnored
	Reason      string // why it was ignored
	OperationID string // the deploy it queued or joined
}

// PushSink records a verified push and enqueues its deploy. It is nil until
// deliveries are recorded (ROADMAP P4.2); the receiver then verifies and
// answers, and ignores every push.
type PushSink interface {
	Push(ctx context.Context, delivery string, p webhook.Push) (PushOutcome, error)
}

type webhookHandlers struct {
	log    *slog.Logger
	secret []byte
	sink   PushSink
}

type webhookReply struct {
	Delivery    string `json:"delivery"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

// github receives GitHub's deliveries: the only public endpoint without a
// token (ARCHITECTURE §7). Nothing in the request is trusted, logged, or
// stored before its signature is verified over the raw body; until then a
// failure is logged only, so unauthenticated traffic cannot fill a table
// (CLAUDE.md invariant 9).
func (h *webhookHandlers) github(w http.ResponseWriter, r *http.Request) {
	if len(h.secret) == 0 {
		writeProblem(w, http.StatusNotFound, "GitHub webhooks are not configured on this server")
		return
	}
	// The server has no read timeout (event streams); this request gets one.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(webhookReadTimeout))
	if r.ContentLength > webhook.MaxPayload {
		h.reject(w, r, http.StatusRequestEntityTooLarge, "the payload is larger than GitHub's 25 MB limit")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhook.MaxPayload))
	if err != nil {
		if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
			h.reject(w, r, http.StatusRequestEntityTooLarge, "the payload is larger than GitHub's 25 MB limit")
			return
		}
		h.reject(w, r, http.StatusBadRequest, "the payload could not be read")
		return
	}
	if err := webhook.Verify(h.secret, body, r.Header.Get(webhook.HeaderSignature)); err != nil {
		h.reject(w, r, http.StatusUnauthorized, err.Error())
		return
	}

	// Verified: the request comes from GitHub with our secret.
	delivery := r.Header.Get(webhook.HeaderDelivery)
	if !webhook.ValidDelivery(delivery) {
		h.reject(w, r, http.StatusBadRequest, "the "+webhook.HeaderDelivery+" header is missing or malformed")
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		h.reject(w, r, http.StatusUnsupportedMediaType, "set the webhook's content type to application/json")
		return
	}
	switch event := r.Header.Get(webhook.HeaderEvent); event {
	case "ping": // sent when the webhook is created [GH-EVENTS]
		h.answer(w, r, http.StatusOK, event, webhookReply{Delivery: delivery, Outcome: OutcomePong}, 0)
		return
	case "push":
	default:
		h.answer(w, r, http.StatusAccepted, event, webhookReply{Delivery: delivery, Outcome: OutcomeIgnored,
			Reason: "only push events are handled"}, 0)
		return
	}
	push, ignore, err := webhook.ParsePush(body)
	switch {
	case err != nil:
		h.reject(w, r, http.StatusBadRequest, err.Error())
		return
	case ignore != "":
		h.answer(w, r, http.StatusAccepted, "push", webhookReply{Delivery: delivery, Outcome: OutcomeIgnored, Reason: ignore}, push.RepositoryID)
		return
	case h.sink == nil:
		h.answer(w, r, http.StatusAccepted, "push", webhookReply{Delivery: delivery, Outcome: OutcomeIgnored,
			Reason: "this server does not deploy from webhooks yet"}, push.RepositoryID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webhookSinkTimeout)
	defer cancel()
	out, err := h.sink.Push(ctx, delivery, push)
	if err != nil {
		// GitHub does not retry by itself [GH-REDELIVER]; a 5xx marks the
		// delivery failed, so the operator can redeliver it.
		h.log.ErrorContext(r.Context(), "webhook delivery not recorded", slog.String("delivery", delivery),
			slog.Int64("repository_id", push.RepositoryID), slog.Any("err", err))
		writeProblem(w, http.StatusServiceUnavailable, "the delivery could not be recorded; redeliver it from GitHub")
		return
	}
	h.answer(w, r, http.StatusAccepted, "push", webhookReply{Delivery: delivery, Outcome: out.Outcome, Reason: out.Reason,
		OperationID: out.OperationID}, push.RepositoryID)
}

// reject answers a delivery that is refused. It logs the reason and, only
// when it is well formed, the delivery id; never the payload.
func (h *webhookHandlers) reject(w http.ResponseWriter, r *http.Request, status int, detail string) {
	attrs := []any{slog.Int("status", status), slog.String("reason", detail)}
	if d := r.Header.Get(webhook.HeaderDelivery); webhook.ValidDelivery(d) {
		attrs = append(attrs, slog.String("delivery", d))
	}
	h.log.WarnContext(r.Context(), "webhook delivery rejected", attrs...)
	writeProblem(w, status, detail)
}

func (h *webhookHandlers) answer(w http.ResponseWriter, r *http.Request, status int, event string, reply webhookReply, repo int64) {
	attrs := []any{slog.String("delivery", reply.Delivery), slog.String("event", fmt.Sprintf("%.64s", event)),
		slog.String("outcome", reply.Outcome)}
	if reply.Reason != "" {
		attrs = append(attrs, slog.String("reason", reply.Reason))
	}
	if repo > 0 {
		attrs = append(attrs, slog.Int64("repository_id", repo))
	}
	if reply.OperationID != "" {
		attrs = append(attrs, slog.String("operation_id", reply.OperationID))
	}
	h.log.InfoContext(r.Context(), "webhook delivery", attrs...)
	writeJSON(w, status, "application/json", reply)
}
