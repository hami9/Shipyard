package api

import (
	"encoding/json"
	"net/http"
)

// Problem is an RFC 9457 problem details body. Detail is shown to clients, so
// it must never contain secrets, SQL, or internal error text.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
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
