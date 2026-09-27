package api

import (
	"encoding/json"
	"net/http"

	"github.com/hami9/shipyard/internal/app"
)

// Problem is an RFC 9457 problem details body. Detail is shown to clients, so
// it must never contain secrets, SQL, or internal error text.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Errors lists invalid fields; an extension member [RFC9457].
	Errors []app.FieldError `json:"errors,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, "application/problem+json", Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
