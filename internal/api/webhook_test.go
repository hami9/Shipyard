package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/logging"
	"github.com/hami9/shipyard/internal/webhook"
)

const hookSecret = "0123456789abcdef-webhook-secret"

type fakePushes struct {
	got []webhook.Push
	ids []string
	out PushOutcome
	err error
}

func (f *fakePushes) Push(ctx context.Context, delivery string, p webhook.Push) (PushOutcome, error) {
	if _, ok := ctx.Deadline(); !ok {
		return PushOutcome{}, errors.New("no deadline")
	}
	f.got, f.ids = append(f.got, p), append(f.ids, delivery)
	return f.out, f.err
}

type hookEnv struct {
	h      http.Handler
	pushes *fakePushes
	tokens *fakeTokens
	audit  *fakeAudit
	logs   *bytes.Buffer
}

func newHookEnv(secret string, sink bool) *hookEnv {
	e := &hookEnv{pushes: &fakePushes{out: PushOutcome{Outcome: OutcomeQueued, OperationID: "op-1"}},
		tokens: newFakeTokens(), audit: &fakeAudit{}, logs: &bytes.Buffer{}}
	d := Deps{DB: fakePinger{}, Tokens: e.tokens, Audit: e.audit, WebhookSecret: []byte(secret)}
	if sink {
		d.Pushes = e.pushes
	}
	e.h = NewHandler(logging.New(e.logs, slog.LevelDebug, logging.FormatJSON), d)
	return e
}

// delivery builds a request as GitHub sends it; sig "" signs with the secret.
func delivery(event, body, sig string, edit func(*http.Request)) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/hooks/github", strings.NewReader(body))
	if sig == "" {
		sig = webhook.Sign([]byte(hookSecret), []byte(body))
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "GitHub-Hookshot/abc")
	r.Header.Set(webhook.HeaderEvent, event)
	r.Header.Set(webhook.HeaderDelivery, "72d3162e-cc78-11e3-81ab-4c9367dc0958")
	r.Header.Set(webhook.HeaderSignature, sig)
	if edit != nil {
		edit(r)
	}
	return r
}

func (e *hookEnv) send(r *http.Request) (*httptest.ResponseRecorder, webhookReply) {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	var reply webhookReply
	json.Unmarshal(rec.Body.Bytes(), &reply)
	return rec, reply
}

const pushBody = `{"ref":"refs/heads/main","before":"1111111111111111111111111111111111111111",` +
	`"after":"0123456789abcdef0123456789abcdef01234567","deleted":false,"repository":{"id":42,"full_name":"octo/app"}}`

// A verified push to a branch reaches the sink with its delivery id; the
// answer is 202 with the sink's outcome.
func TestWebhookPush(t *testing.T) {
	e := newHookEnv(hookSecret, true)
	rec, reply := e.send(delivery("push", pushBody, "", nil))
	if rec.Code != http.StatusAccepted || reply.Outcome != OutcomeQueued || reply.OperationID != "op-1" ||
		reply.Delivery != "72d3162e-cc78-11e3-81ab-4c9367dc0958" {
		t.Fatalf("%d %+v", rec.Code, reply)
	}
	want := webhook.Push{RepositoryID: 42, Repository: "octo/app", Ref: "refs/heads/main", Branch: "main",
		After: "0123456789abcdef0123456789abcdef01234567"}
	if len(e.pushes.got) != 1 || e.pushes.got[0] != want || e.pushes.ids[0] != reply.Delivery {
		t.Fatalf("sink got %+v %v", e.pushes.got, e.pushes.ids)
	}
	if len(e.audit.events) != 0 || e.tokens.lookups != 0 {
		t.Fatalf("audit %v, token lookups %d", e.audit.events, e.tokens.lookups)
	}
	if strings.Contains(e.logs.String(), "octo/app") || !strings.Contains(e.logs.String(), `"repository_id":42`) {
		t.Fatalf("log should name the repository by id only:\n%s", e.logs)
	}

	// A sink that fails is a 503, so GitHub marks the delivery failed.
	e.pushes.err = errors.New("db down")
	if rec, _ := e.send(delivery("push", pushBody, "", nil)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sink error: %d", rec.Code)
	}
}

// Deliveries that cannot deploy are answered 2XX and never reach the sink.
func TestWebhookIgnored(t *testing.T) {
	for name, tc := range map[string]struct {
		event, body string
		sink        bool
		status      int
		outcome     string
		reason      string
	}{
		"ping":        {"ping", `{"zen":"Keep it logically awesome.","hook_id":1}`, true, http.StatusOK, OutcomePong, ""},
		"other event": {"issues", `{"action":"opened"}`, true, http.StatusAccepted, OutcomeIgnored, "only push events are handled"},
		"no event":    {"", `{}`, true, http.StatusAccepted, OutcomeIgnored, "only push events are handled"},
		"tag": {"push", `{"ref":"refs/tags/v1","after":"0123456789abcdef0123456789abcdef01234567","repository":{"id":1}}`,
			true, http.StatusAccepted, OutcomeIgnored, "the ref is not a branch"},
		"deleted": {"push", `{"ref":"refs/heads/x","after":"0000000000000000000000000000000000000000","deleted":true,"repository":{"id":1}}`,
			true, http.StatusAccepted, OutcomeIgnored, "the push deleted the ref"},
		"no sink": {"push", pushBody, false, http.StatusAccepted, OutcomeIgnored, "this server does not deploy from webhooks yet"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newHookEnv(hookSecret, tc.sink)
			rec, reply := e.send(delivery(tc.event, tc.body, "", nil))
			if rec.Code != tc.status || reply.Outcome != tc.outcome || reply.Reason != tc.reason {
				t.Fatalf("%d %+v", rec.Code, reply)
			}
			if len(e.pushes.got) != 0 {
				t.Fatalf("sink called: %+v", e.pushes.got)
			}
		})
	}
}

// Negative tests (CLAUDE.md §5): a delivery that fails verification, or
// that is verified but malformed, is refused before anything is recorded.
func TestWebhookRejects(t *testing.T) {
	big := `{"x":"` + strings.Repeat("a", webhook.MaxPayload) + `"}`
	for name, tc := range map[string]struct {
		req    *http.Request
		status int
	}{
		"no signature": {delivery("push", pushBody, "", func(r *http.Request) { r.Header.Del(webhook.HeaderSignature) }), http.StatusUnauthorized},
		"wrong secret": {delivery("push", pushBody, webhook.Sign([]byte("another-secret-of-length"), []byte(pushBody)), nil), http.StatusUnauthorized},
		"tampered body": {delivery("push", pushBody, webhook.Sign([]byte(hookSecret), []byte(strings.Replace(pushBody, "main", "prod", 1))), nil),
			http.StatusUnauthorized},
		"sha1 only": {delivery("push", pushBody, "", func(r *http.Request) {
			r.Header.Del(webhook.HeaderSignature)
			r.Header.Set("X-Hub-Signature", "sha1=0000000000000000000000000000000000000000")
		}), http.StatusUnauthorized},
		"bad signature, bad delivery": {delivery("push", pushBody, "sha256=00", func(r *http.Request) { r.Header.Del(webhook.HeaderDelivery) }),
			http.StatusUnauthorized},
		"too large":         {delivery("push", big, "sha256=00", nil), http.StatusRequestEntityTooLarge},
		"too large, signed": {delivery("push", big, "", nil), http.StatusRequestEntityTooLarge},
		"too large, no length": {delivery("push", big, "", func(r *http.Request) {
			r.ContentLength = -1
			r.Body = io.NopCloser(strings.NewReader(big))
		}), http.StatusRequestEntityTooLarge},
		"no delivery":      {delivery("push", pushBody, "", func(r *http.Request) { r.Header.Del(webhook.HeaderDelivery) }), http.StatusBadRequest},
		"bad delivery":     {delivery("push", pushBody, "", func(r *http.Request) { r.Header.Set(webhook.HeaderDelivery, "../x") }), http.StatusBadRequest},
		"form encoded":     {delivery("push", pushBody, "", func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }), http.StatusUnsupportedMediaType},
		"not json":         {delivery("push", `payload=x`, "", nil), http.StatusBadRequest},
		"no repository id": {delivery("push", `{"ref":"refs/heads/main","after":"0123456789abcdef0123456789abcdef01234567"}`, "", nil), http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			e := newHookEnv(hookSecret, true)
			rec, _ := e.send(tc.req)
			if rec.Code != tc.status || rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status %d (%s), want %d: %s", rec.Code, rec.Header().Get("Content-Type"), tc.status, rec.Body)
			}
			if len(e.pushes.got) != 0 || len(e.audit.events) != 0 {
				t.Fatalf("sink %v, audit %v", e.pushes.got, e.audit.events)
			}
			if logs := e.logs.String(); strings.Contains(logs, "octo/app") || strings.Contains(logs, hookSecret) || strings.Contains(logs, "../x") {
				t.Fatalf("the log shows request data:\n%s", logs)
			}
		})
	}
}

// Without a secret the endpoint does not exist, whatever the request; GET
// is not routed.
func TestWebhookNotConfigured(t *testing.T) {
	e := newHookEnv("", true)
	if rec, _ := e.send(delivery("push", pushBody, "", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("no secret: %d", rec.Code)
	}
	e = newHookEnv(hookSecret, true)
	if rec := do(e.h, http.MethodGet, "/hooks/github", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}
