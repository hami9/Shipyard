package openapits

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite web/src/api/schema.ts")

const (
	specFile   = "../../api/openapi.json"
	outputFile = "../../web/src/api/schema.ts"
)

// The committed TypeScript matches the spec. After a spec change:
// make web-types.
func TestGenerated(t *testing.T) {
	spec, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Generate(spec)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(outputFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("%v: run make web-types", err)
	}
	if !bytes.Equal(got, bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
		t.Fatalf("%s is stale: run make web-types", outputFile)
	}
}

func generate(t *testing.T, spec string) string {
	t.Helper()
	out, err := Generate([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestSchemas(t *testing.T) {
	out := generate(t, `{"paths": {}, "components": {"schemas": {
		"Thing": {"type": "object", "required": ["id", "maybe"], "additionalProperties": false, "description": "A thing",
			"properties": {
				"id": {"$ref": "#/components/schemas/ID", "description": "Its ID"},
				"maybe": {"type": ["string", "null"], "format": "date-time"},
				"level": {"type": "string", "enum": ["info", "warn"]},
				"tags": {"type": "array", "items": {"type": ["string", "null"]}},
				"nested": {"type": "object", "properties": {"n": {"type": "integer"}}},
				"free": {"type": "object"},
				"none": {"type": "object", "additionalProperties": false},
				"Content-Type": {"const": "ok"}
			}},
		"ID": {"type": "string", "pattern": "^x$"}
	}}}`)
	for _, want := range []string{
		"/** A thing */\nexport interface Thing {\n",
		"  /** Its ID */\n  id: ID;\n",
		"  maybe: string | null;\n",
		`  level?: "info" | "warn";` + "\n",
		"  tags?: (string | null)[];\n",
		"  nested?: {\n    n?: number;\n  };\n",
		"  free?: Record<string, unknown>;\n",
		"  none?: Record<string, never>;\n",
		`  "Content-Type"?: "ok";` + "\n",
		"export type ID = string;\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestOperations(t *testing.T) {
	out := generate(t, `{"paths": {
		"/v1/things/{id}": {
			"parameters": [{"$ref": "#/components/parameters/ID"}],
			"get": {"operationId": "getThing", "summary": "Show a thing",
				"parameters": [{"name": "limit", "in": "query", "schema": {"type": "integer"}},
					{"name": "Idempotency-Key", "in": "header", "schema": {"type": "string"}}],
				"responses": {"200": {"description": "", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/T"}}}},
					"404": {"description": "", "content": {"application/problem+json": {"schema": {"type": "object"}}}}}},
			"delete": {"operationId": "dropThing", "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/T"}}}},
				"responses": {"204": {"description": "gone"}}}
		},
		"/v1/things/{id}/events": {
			"get": {"operationId": "watch", "parameters": [{"$ref": "#/components/parameters/ID"}],
				"responses": {"200": {"$ref": "#/components/responses/Stream"}}}
		},
		"/v1/ping": {"post": {"operationId": "ping", "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/T"}}}},
			"responses": {"200": {"$ref": "#/components/responses/T"}, "202": {"$ref": "#/components/responses/T"}}}}
	}, "components": {
		"schemas": {"T": {"type": "string"}},
		"parameters": {"ID": {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}},
		"responses": {
			"T": {"description": "", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/T"}}}},
			"Stream": {"description": "", "content": {"text/event-stream": {"schema": {"type": "string"},
				"x-events": {"message": {"$ref": "#/components/schemas/T"}, "end": {"type": "object"}}}}}
		}
	}}`)
	for _, want := range []string{
		"  /** Show a thing */\n  getThing: {\n" +
			`    request: { path: { id: string }; query?: { limit?: number }; header?: { "Idempotency-Key"?: string } };` + "\n" +
			"    response: T;\n    events: never;\n  };\n",
		"  dropThing: {\n    request: { path: { id: string }; body: T };\n    response: void;\n",
		"    response: never;\n    events: { message: T; end: Record<string, unknown> };\n",
		"  ping: {\n    request: { body?: T };\n    response: T;\n",
		`  getThing: { method: "GET", path: "/v1/things/{id}", stream: false },`,
		`  watch: { method: "GET", path: "/v1/things/{id}/events", stream: true },`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A keyword the generator does not know is refused, not silently dropped.
func TestRefusesWhatItCannotType(t *testing.T) {
	for name, spec := range map[string]string{
		"oneOf":          `{"paths": {}, "components": {"schemas": {"X": {"oneOf": [{"type": "string"}]}}}}`,
		"foreign ref":    `{"paths": {}, "components": {"schemas": {"X": {"$ref": "other.json#/X"}}}}`,
		"no type":        `{"paths": {}, "components": {"schemas": {"X": {"description": "?"}}}}`,
		"no operationId": `{"paths": {"/x": {"get": {"responses": {}}}}, "components": {}}`,
		"stray required": `{"paths": {}, "components": {"schemas": {"X": {"type": "object", "required": ["y"], "properties": {"z": {"type": "string"}}}}}}`,
		"duplicate key":  `{"paths": {}, "paths": {}}`,
	} {
		if _, err := Generate([]byte(spec)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
