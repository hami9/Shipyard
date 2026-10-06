// Package openapits generates the web UI's TypeScript types from
// api/openapi.json (ADR-0015): a type per component schema, and an
// Operations map, keyed by operationId, that the hand-written client in
// web/src/api is typed by. The output is committed; TestGenerated fails
// when it is stale, and `make web-types` rewrites it.
//
// It covers the JSON Schema subset the spec uses, the same one
// internal/api's tests validate, and refuses any other keyword rather
// than ignore it.
package openapits

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Header is the generated file's first line.
const Header = "// Code generated from api/openapi.json by internal/openapits (make web-types). DO NOT EDIT."

var schemaKeys = []string{"$ref", "type", "enum", "const", "required", "properties", "additionalProperties", "items",
	"format", "pattern", "minimum", "maximum", "exclusiveMinimum", "minLength", "maxLength", "description", "default", "examples"}

var methods = []string{"get", "put", "post", "delete", "patch"}

// Generate returns the TypeScript module for spec.
func Generate(spec []byte) ([]byte, error) {
	v, err := parse(spec)
	if err != nil {
		return nil, err
	}
	doc, ok := v.(*object)
	if !ok {
		return nil, errors.New("the spec is not a JSON object")
	}
	g := &gen{doc: doc}
	g.line(Header)
	g.line("// The API's schemas, and its operations by operationId. The client is web/src/api/client.ts.")
	schemas := doc.obj("components").obj("schemas")
	for _, name := range schemas.names() {
		g.line("")
		g.named(name, schemas.obj(name))
	}
	g.operations()
	return []byte(g.b.String()), errors.Join(g.errs...)
}

type gen struct {
	doc  *object
	b    strings.Builder
	errs []error
}

func (g *gen) line(s string) { g.b.WriteString(s + "\n") }

func (g *gen) fail(at string, format string, args ...any) {
	g.errs = append(g.errs, fmt.Errorf("%s: %s", at, fmt.Sprintf(format, args...)))
}

// comment is a description as JSDoc, or "".
func comment(indent string, s *object) string {
	if d, _ := s.get("description").(string); d != "" {
		return indent + "/** " + strings.ReplaceAll(d, "*/", "*\\/") + " */\n"
	}
	return ""
}

// named writes a component schema: an interface for an object with
// properties, a type alias for anything else.
func (g *gen) named(name string, s *object) {
	at := "#/components/schemas/" + name
	g.b.WriteString(comment("", s))
	if s.get("properties") != nil && s.get("$ref") == nil {
		g.line("export interface " + name + " " + g.object(at, s, ""))
		return
	}
	g.line("export type " + name + " = " + g.expr(at, s, "") + ";")
}

// expr is the TypeScript type of schema s, written at indent.
func (g *gen) expr(at string, s *object, indent string) string {
	if s == nil {
		g.fail(at, "missing schema")
		return "unknown"
	}
	for _, k := range s.keys {
		if !slices.Contains(schemaKeys, k) {
			g.fail(at, "keyword %q is not supported: add it to internal/openapits and internal/api's validator first", k)
		}
	}
	if ref, ok := s.get("$ref").(string); ok {
		name, ok := strings.CutPrefix(ref, "#/components/schemas/")
		if !ok {
			g.fail(at, "$ref %s: only component schemas are supported", ref)
		}
		return name
	}
	if c := s.get("const"); c != nil {
		return literal(c)
	}
	if enum, ok := s.get("enum").([]any); ok {
		parts := make([]string, len(enum))
		for i, e := range enum {
			parts[i] = literal(e)
		}
		return strings.Join(parts, " | ")
	}
	var types []string
	switch t := s.get("type").(type) {
	case string:
		types = []string{t}
	case []any:
		for _, e := range t {
			name, _ := e.(string)
			types = append(types, name)
		}
	case nil:
		if s.get("properties") != nil {
			types = []string{"object"}
		}
	}
	if len(types) == 0 {
		g.fail(at, "a schema needs a type, $ref, const, or enum")
		return "unknown"
	}
	parts := make([]string, 0, len(types))
	for _, t := range types {
		switch t {
		case "string":
			parts = append(parts, "string")
		case "integer", "number":
			parts = append(parts, "number")
		case "boolean":
			parts = append(parts, "boolean")
		case "null":
			parts = append(parts, "null")
		case "array":
			item := g.expr(at+"/items", s.obj("items"), indent)
			if strings.Contains(item, " | ") {
				item = "(" + item + ")"
			}
			parts = append(parts, item+"[]")
		case "object":
			parts = append(parts, g.object(at, s, indent))
		default:
			g.fail(at, "unknown type %q", t)
		}
	}
	return strings.Join(slices.Compact(parts), " | ")
}

// object writes an object type's body. Without properties it is a record:
// of nothing when additionalProperties is false, of unknown values if not.
func (g *gen) object(at string, s *object, indent string) string {
	props := s.obj("properties")
	if props == nil || len(props.keys) == 0 {
		if s.get("additionalProperties") == false {
			return "Record<string, never>"
		}
		return "Record<string, unknown>"
	}
	var required []string
	if r, ok := s.get("required").([]any); ok {
		for _, e := range r {
			name, _ := e.(string)
			required = append(required, name)
		}
	}
	var b strings.Builder
	b.WriteString("{\n")
	inner := indent + "  "
	for _, name := range props.keys {
		p := props.obj(name)
		b.WriteString(comment(inner, p))
		opt := "?"
		if slices.Contains(required, name) {
			opt = ""
		}
		b.WriteString(inner + key(name) + opt + ": " + g.expr(at+"/properties/"+name, p, inner) + ";\n")
	}
	for _, name := range required {
		if !slices.Contains(props.keys, name) {
			g.fail(at, "required %q is not a property", name)
		}
	}
	b.WriteString(indent + "}")
	return b.String()
}

type param struct {
	name, in string
	required bool
	schema   *object
}

// operations writes the Operations interface and the operations constant.
func (g *gen) operations() {
	type op struct {
		id, method, path string
		stream           bool
	}
	var ops []op
	g.line("")
	g.line("/** Every operation: its request (path, query, header, body), its success response, and a stream's events. */")
	g.line("export interface Operations {")
	paths := g.doc.obj("paths")
	for _, path := range paths.names() {
		item := paths.obj(path)
		for _, method := range methods {
			o := item.obj(method)
			if o == nil {
				continue
			}
			at := method + " " + path
			id, _ := o.get("operationId").(string)
			if id == "" {
				g.fail(at, "no operationId")
				continue
			}
			if s, _ := o.get("summary").(string); s != "" {
				g.line("  /** " + s + " */")
			}
			g.line("  " + id + ": {")
			g.line("    request: " + g.request(at, item, o) + ";")
			response, events := g.responses(at, o)
			ops = append(ops, op{id, strings.ToUpper(method), path, events != "never"})
			g.line("    response: " + response + ";")
			g.line("    events: " + events + ";")
			g.line("  };")
		}
	}
	g.line("}")
	g.line("")
	g.line("/** Each operation's method and path template, and whether it answers with an event stream. */")
	g.line("export const operations = {")
	for _, o := range ops {
		g.line(fmt.Sprintf("  %s: { method: %q, path: %q, stream: %t },", o.id, o.method, o.path, o.stream))
	}
	g.line("} as const satisfies { [K in keyof Operations]: { method: string; path: string; stream: boolean } };")
}

// request is an operation's request type: the path, query, and header
// parameters by location, and its JSON body.
func (g *gen) request(at string, item, o *object) string {
	var params []param
	add := func(list any) {
		l, _ := list.([]any)
		for _, e := range l {
			p := g.param(at, e)
			if i := slices.IndexFunc(params, func(q param) bool { return q.name == p.name && q.in == p.in }); i >= 0 {
				params[i] = p // an operation's parameter overrides its path's
			} else {
				params = append(params, p)
			}
		}
	}
	add(item.get("parameters"))
	add(o.get("parameters"))
	var members []string
	for _, in := range []string{"path", "query", "header"} {
		var fields []string
		anyRequired := false
		for _, p := range params {
			if p.in != in {
				continue
			}
			opt := "?"
			if p.required {
				opt, anyRequired = "", true
			}
			fields = append(fields, key(p.name)+opt+": "+g.expr(at+" "+p.name, p.schema, "      "))
		}
		if len(fields) == 0 {
			continue
		}
		opt := "?"
		if anyRequired {
			opt = ""
		}
		members = append(members, in+opt+": { "+strings.Join(fields, "; ")+" }")
	}
	if body := o.obj("requestBody"); body != nil {
		s := body.obj("content").obj("application/json").obj("schema")
		opt := "?"
		if body.get("required") == true {
			opt = ""
		}
		members = append(members, "body"+opt+": "+g.expr(at+" body", s, "    "))
	}
	if len(members) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(members, "; ") + " }"
}

func (g *gen) param(at string, e any) param {
	p, _ := e.(*object)
	if ref, ok := p.get("$ref").(string); ok {
		name, _ := strings.CutPrefix(ref, "#/components/parameters/")
		p = g.doc.obj("components").obj("parameters").obj(name)
		if p == nil {
			g.fail(at, "parameter %s does not resolve", ref)
			return param{}
		}
	}
	name, _ := p.get("name").(string)
	in, _ := p.get("in").(string)
	return param{name: name, in: in, required: p.get("required") == true, schema: p.obj("schema")}
}

// responses is the union of an operation's success bodies (void for none),
// and, for an event stream, its events by name.
func (g *gen) responses(at string, o *object) (response, events string) {
	rs := o.obj("responses")
	var types []string
	events = "never"
	for _, status := range rs.names() {
		if !strings.HasPrefix(status, "2") {
			continue
		}
		r := rs.obj(status)
		if ref, ok := r.get("$ref").(string); ok {
			name, _ := strings.CutPrefix(ref, "#/components/responses/")
			r = g.doc.obj("components").obj("responses").obj(name)
		}
		content := r.obj("content")
		switch {
		case content == nil || len(content.keys) == 0:
			types = append(types, "void")
		case content.obj("application/json") != nil:
			types = append(types, g.expr(at+" "+status, content.obj("application/json").obj("schema"), "    "))
		case content.obj("text/event-stream") != nil:
			ev := content.obj("text/event-stream").obj("x-events")
			if ev == nil {
				g.fail(at, "an event stream without x-events")
				continue
			}
			var fields []string
			for _, name := range ev.keys {
				fields = append(fields, key(name)+": "+g.expr(at+" event "+name, ev.obj(name), "    "))
			}
			events = "{ " + strings.Join(fields, "; ") + " }"
		default:
			g.fail(at, "status %s: unsupported content %v", status, content.keys)
		}
	}
	if len(types) == 0 {
		return "never", events
	}
	return strings.Join(slices.Compact(types), " | "), events
}

var identRE = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func key(name string) string {
	if identRE.MatchString(name) {
		return name
	}
	return literal(name)
}

func literal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// object is a JSON object that keeps its keys' order, so the generated
// types read in the spec's order.
type object struct {
	keys []string
	vals map[string]any
}

func (o *object) get(k string) any {
	if o == nil {
		return nil
	}
	return o.vals[k]
}

func (o *object) obj(k string) *object {
	v, _ := o.get(k).(*object)
	return v
}

// names is o's keys in order; none for a missing object.
func (o *object) names() []string {
	if o == nil {
		return nil
	}
	return o.keys
}

func parse(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := value(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("trailing data after the spec")
	}
	return v, nil
}

func value(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		o := &object{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k := kt.(string)
			v, err := value(dec)
			if err != nil {
				return nil, err
			}
			if _, dup := o.vals[k]; dup {
				return nil, fmt.Errorf("duplicate key %q", k)
			}
			o.keys = append(o.keys, k)
			o.vals[k] = v
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return o, nil
	case json.Delim('['):
		var l []any
		for dec.More() {
			v, err := value(dec)
			if err != nil {
				return nil, err
			}
			l = append(l, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return l, nil
	}
	return t, nil
}
