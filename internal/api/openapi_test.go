package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// api/openapi.json is written by hand (P6.1a). These tests keep it true:
// it lists exactly newMux's routes with their scopes, and every response
// the package's tests provoke, and every request body that succeeded, fits
// its schemas (conform). With -tags integration, TestMain also requires
// every success response in it to have been seen.

const specPath = "../../api/openapi.json"

type openAPI struct {
	raw        any
	ops        map[string]*oaOperation // "GET /v1/apps/{app}"
	schemas    map[string]*schema
	responses  map[string]*oaResponse
	mux        *http.ServeMux // the spec's paths, to match requests
	defaultSec []map[string][]string
}

type oaOperation struct {
	OperationID string                 `json:"operationId"`
	Security    *[]map[string][]string `json:"security"`
	RequestBody *oaBody                `json:"requestBody"`
	Responses   map[string]*oaResponse `json:"responses"`
}

type oaBody struct {
	Required bool               `json:"required"`
	Content  map[string]oaMedia `json:"content"`
}

type oaResponse struct {
	Ref     string             `json:"$ref"`
	Content map[string]oaMedia `json:"content"`
}

type oaMedia struct {
	Schema *schema            `json:"schema"`
	Events map[string]*schema `json:"x-events"` // SSE: the data of each event name
}

// schema is the subset of JSON Schema 2020-12 the spec uses.
type schema struct {
	Ref                  string             `json:"$ref"`
	Type                 json.RawMessage    `json:"type"` // a name or a list of names
	Enum                 []any              `json:"enum"`
	Const                json.RawMessage    `json:"const"`
	Required             []string           `json:"required"`
	Properties           map[string]*schema `json:"properties"`
	AdditionalProperties *bool              `json:"additionalProperties"`
	Items                *schema            `json:"items"`
	Format               string             `json:"format"`
	Pattern              string             `json:"pattern"`
	Minimum              *float64           `json:"minimum"`
	Maximum              *float64           `json:"maximum"`
	ExclusiveMinimum     *float64           `json:"exclusiveMinimum"`
	MinLength            *int               `json:"minLength"`
	MaxLength            *int               `json:"maxLength"`
}

var loadSpec = sync.OnceValues(func() (*openAPI, error) {
	b, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Security   []map[string][]string                 `json:"security"`
		Components struct {
			Schemas   map[string]*schema     `json:"schemas"`
			Responses map[string]*oaResponse `json:"responses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", specPath, err)
	}
	s := &openAPI{ops: map[string]*oaOperation{}, schemas: doc.Components.Schemas, responses: doc.Components.Responses,
		mux: http.NewServeMux(), defaultSec: doc.Security}
	if err := json.Unmarshal(b, &s.raw); err != nil {
		return nil, err
	}
	for path, item := range doc.Paths {
		for method, raw := range item {
			if method == "parameters" {
				continue
			}
			var op oaOperation
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, fmt.Errorf("%s %s: %w", method, path, err)
			}
			pattern := strings.ToUpper(method) + " " + path
			s.ops[pattern] = &op
			s.mux.Handle(pattern, http.NotFoundHandler())
		}
	}
	return s, nil
})

func spec(t testing.TB) *openAPI {
	t.Helper()
	s, err := loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// scope is the scope an operation's security requirement names; "" when
// it has none (public).
func (s *openAPI) scope(op *oaOperation) string {
	sec := s.defaultSec
	if op.Security != nil {
		sec = *op.Security
	}
	for _, req := range sec {
		for _, scopes := range req {
			return strings.Join(scopes, ",")
		}
	}
	return ""
}

func (s *openAPI) response(r *oaResponse) *oaResponse {
	if name, ok := strings.CutPrefix(r.Ref, "#/components/responses/"); ok {
		return s.responses[name]
	}
	return r
}

func (s *openAPI) resolve(sc *schema) *schema {
	for sc != nil && sc.Ref != "" {
		sc = s.schemas[strings.TrimPrefix(sc.Ref, "#/components/schemas/")]
	}
	return sc
}

// The routes newMux registers that the spec leaves out on purpose.
var unspecifiedRoutes = map[string]string{
	"POST /hooks/github": "GitHub's webhook contract, verified by its signature, not a token",
}

func TestOpenAPIRoutes(t *testing.T) {
	s := spec(t)
	_, routes := newMux(slog.New(slog.DiscardHandler), Deps{})
	registered := map[string]string{}
	for _, r := range routes {
		if _, skip := unspecifiedRoutes[r.pattern]; !skip {
			registered[r.pattern] = r.scope
		}
	}
	for pattern, scope := range registered {
		op, ok := s.ops[pattern]
		if !ok {
			t.Errorf("%s is served but not in %s", pattern, specPath)
			continue
		}
		if got := s.scope(op); got != scope {
			t.Errorf("%s: the spec requires scope %q, the server %q", pattern, got, scope)
		}
	}
	for pattern := range s.ops {
		if _, ok := registered[pattern]; !ok {
			t.Errorf("%s is in %s but not served", pattern, specPath)
		}
	}
}

// Every $ref resolves, and operation IDs are unique (the TypeScript client
// is named after them).
func TestOpenAPIWellFormed(t *testing.T) {
	s := spec(t)
	var walk func(v any, at string)
	walk = func(v any, at string) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok && pointer(s.raw, ref) == nil {
				t.Errorf("%s: $ref %s does not resolve", at, ref)
			}
			for k, c := range v {
				walk(c, at+"/"+k)
			}
		case []any:
			for i, c := range v {
				walk(c, at+"/"+strconv.Itoa(i))
			}
		}
	}
	walk(s.raw, "#")
	ids := map[string]string{}
	for pattern, op := range s.ops {
		if op.OperationID == "" {
			t.Errorf("%s has no operationId", pattern)
		} else if other, dup := ids[op.OperationID]; dup {
			t.Errorf("operationId %s is used by %s and %s", op.OperationID, pattern, other)
		}
		ids[op.OperationID] = pattern
		if len(op.Responses) == 0 {
			t.Errorf("%s has no responses", pattern)
		}
	}
}

func pointer(doc any, ref string) any {
	p, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil
	}
	for _, part := range strings.Split(p, "/") {
		m, ok := doc.(map[string]any)
		if !ok {
			return nil
		}
		doc = m[part]
	}
	return doc
}

// The validator itself, on cases where it must say no.
func TestSchemaValidator(t *testing.T) {
	s := spec(t)
	app := &schema{Ref: "#/components/schemas/App"}
	good := `{"id":"0b6e1c1e-2a0c-4bde-9c43-0d3f1c2b9a10","slug":"web","repo":"o/r","branch":"main","dockerfile_path":"Dockerfile",
		"build_context":".","port":8080,"health_path":"/","health_timeout":"1m0s","cpu_limit":1,"memory_limit":536870912,
		"stop_timeout":"10s","auto_deploy":false,"github_installation_id":null,"created_at":"2026-10-06T10:00:00Z","updated_at":"2026-10-06T10:00:00Z"}`
	if errs := s.validateJSON(app, []byte(good)); len(errs) > 0 {
		t.Fatalf("a valid app: %v", errs)
	}
	for name, mutate := range map[string]func(map[string]any){
		"unknown field":      func(m map[string]any) { m["extra"] = 1 },
		"missing field":      func(m map[string]any) { delete(m, "branch") },
		"wrong type":         func(m map[string]any) { m["port"] = "8080" },
		"fraction":           func(m map[string]any) { m["port"] = 80.5 },
		"null not allowed":   func(m map[string]any) { m["branch"] = nil },
		"bad pattern":        func(m map[string]any) { m["slug"] = "Web" },
		"bad date":           func(m map[string]any) { m["created_at"] = "yesterday" },
		"bad nested pattern": func(m map[string]any) { m["id"] = "42" },
	} {
		var m map[string]any
		json.Unmarshal([]byte(good), &m)
		mutate(m)
		b, _ := json.Marshal(m)
		if errs := s.validateJSON(app, b); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	if errs := s.validateJSON(&schema{Ref: "#/components/schemas/Token"}, []byte(`{"prefix":"shp_abcd","name":"n","scopes":["root"],
		"status":"active","expires_at":null,"last_used_at":null,"revoked_at":null,"created_at":"2026-10-06T10:00:00Z"}`)); len(errs) == 0 {
		t.Error("an unknown scope (enum) was accepted")
	}
}

func (s *openAPI) validateJSON(sc *schema, b []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	return s.validate(sc, v, "$")
}

var patterns sync.Map // string → *regexp.Regexp

func (s *openAPI) validate(sc *schema, v any, at string) []string {
	sc = s.resolve(sc)
	if sc == nil {
		return []string{at + ": unresolved schema"}
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, at+": "+fmt.Sprintf(format, args...)) }
	if types := typeNames(sc.Type); len(types) > 0 && !slices.Contains(types, jsonType(v)) &&
		!(jsonType(v) == "integer" && slices.Contains(types, "number")) {
		fail("%s, want %s", jsonType(v), strings.Join(types, " or "))
		return errs
	}
	if len(sc.Const) > 0 {
		var c any
		json.Unmarshal(sc.Const, &c)
		if fmt.Sprint(c) != fmt.Sprint(v) {
			fail("%v, want %v", v, c)
		}
	}
	if len(sc.Enum) > 0 && !slices.ContainsFunc(sc.Enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) }) {
		fail("%v is not one of %v", v, sc.Enum)
	}
	switch v := v.(type) {
	case map[string]any:
		for _, k := range sc.Required {
			if _, ok := v[k]; !ok {
				fail("missing %q", k)
			}
		}
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if p, ok := sc.Properties[k]; ok {
				errs = append(errs, s.validate(p, v[k], at+"."+k)...)
			} else if sc.AdditionalProperties != nil && !*sc.AdditionalProperties {
				fail("unexpected %q", k)
			}
		}
	case []any:
		if sc.Items != nil {
			for i, e := range v {
				errs = append(errs, s.validate(sc.Items, e, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	case string:
		if sc.Pattern != "" {
			re, _ := patterns.LoadOrStore(sc.Pattern, regexp.MustCompile(sc.Pattern))
			if !re.(*regexp.Regexp).MatchString(v) {
				fail("%q does not match %s", v, sc.Pattern)
			}
		}
		if sc.Format == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
				fail("%q is not a date-time", v)
			}
		}
		if sc.MinLength != nil && len([]rune(v)) < *sc.MinLength || sc.MaxLength != nil && len([]rune(v)) > *sc.MaxLength {
			fail("length %d out of range", len([]rune(v)))
		}
	case json.Number:
		f, _ := v.Float64()
		if sc.Minimum != nil && f < *sc.Minimum || sc.Maximum != nil && f > *sc.Maximum ||
			sc.ExclusiveMinimum != nil && f <= *sc.ExclusiveMinimum {
			fail("%s out of range", v)
		}
	}
	return errs
}

func typeNames(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	json.Unmarshal(raw, &many)
	return many
}

func jsonType(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if _, err := v.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

// seen records each operation and status the tests provoked, for TestMain.
var seen sync.Map // "GET /v1/apps 200" → true

// conform wraps h so every request the spec describes is checked when its
// handler returns: the status is listed, the body fits the schema (each
// event's data, for a stream), and a request body that succeeded fits too.
// Requests the spec does not describe (unknown routes) pass unchecked.
func conform(t testing.TB, h http.Handler) http.Handler {
	s := spec(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := s.mux.Handler(r)
		op := s.ops[pattern]
		if op == nil {
			h.ServeHTTP(w, r)
			return
		}
		var reqBody []byte
		if r.Body != nil {
			reqBody, _ = io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
			r.Body = io.NopCloser(bytes.NewReader(reqBody))
		}
		tw := &teeWriter{ResponseWriter: w}
		h.ServeHTTP(tw, r)
		if tw.status == 0 {
			tw.status = http.StatusOK
		}
		seen.Store(pattern+" "+strconv.Itoa(tw.status), true)
		for _, e := range s.check(op, reqBody, tw) {
			t.Errorf("%s %s → %d: %s", r.Method, r.URL.Path, tw.status, e)
		}
	})
}

func (s *openAPI) check(op *oaOperation, reqBody []byte, tw *teeWriter) []string {
	resp := op.Responses[strconv.Itoa(tw.status)]
	if resp == nil && tw.status >= 400 {
		resp = op.Responses["default"]
	}
	if resp == nil {
		return []string{"status not in the spec"}
	}
	resp = s.response(resp)
	var errs []string
	if tw.status >= 200 && tw.status < 300 && op.RequestBody != nil {
		switch {
		case len(reqBody) > 0:
			for _, e := range s.validateJSON(op.RequestBody.Content["application/json"].Schema, reqBody) {
				errs = append(errs, "request "+e)
			}
		case op.RequestBody.Required:
			errs = append(errs, "succeeded without the body the spec requires")
		}
	}
	if tw.status >= 200 && tw.status < 300 && op.RequestBody == nil && len(reqBody) > 0 {
		errs = append(errs, "succeeded with a body the spec does not describe")
	}
	if len(resp.Content) == 0 {
		if tw.body.Len() > 0 {
			errs = append(errs, "a body the spec does not describe")
		}
		return errs
	}
	mt, _, _ := mime.ParseMediaType(tw.Header().Get("Content-Type"))
	media, ok := resp.Content[mt]
	if !ok {
		return append(errs, fmt.Sprintf("content type %q, want one of %v", mt, slices.Sorted(maps.Keys(resp.Content))))
	}
	if mt == "text/event-stream" {
		return append(errs, s.checkEvents(media, tw.body.Bytes())...)
	}
	return append(errs, s.validateJSON(media.Schema, tw.body.Bytes())...)
}

// checkEvents validates each complete event's data against the schema for
// its name ("message" when unnamed) [WHATWG-SSE]. A stream the client left
// may end mid-event; that tail is ignored.
func (s *openAPI) checkEvents(media oaMedia, body []byte) []string {
	var errs []string
	name, data := "message", ""
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data != "" {
				if es, ok := media.Events[name]; !ok {
					errs = append(errs, "event "+name+" is not in the spec")
				} else {
					for _, e := range s.validateJSON(es, []byte(data)) {
						errs = append(errs, "event "+name+" "+e)
					}
				}
			}
			name, data = "message", ""
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data += strings.TrimPrefix(line, "data: ")
		}
	}
	return errs
}

// teeWriter passes a response through and keeps a copy. Unwrap lets
// http.ResponseController flush the stream underneath.
type teeWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *teeWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *teeWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *teeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
