# ADR-0015: A hand-written OpenAPI description, tested against the handlers, and a TypeScript client generated in Go

- **Status:** Accepted
- **Date:** 2026-10-06
- **Deciders:** Project owner (spec format and client approach chosen 2026-10-06), Claude Code
- **Sources:** `OAS-31`, `WHATWG-SSE`, `NODE-TS`, `TS-7`

## Context

- **Roadmap P6.1:** an OpenAPI description of `/v1` and a typed TypeScript client, for the web UI (P6.2–P6.6).
- **The API is small:** 21 routes on `net/http`'s `ServeMux`, with no router framework or annotations to generate a spec from (CLAUDE.md §4).
- **A spec that drifts is worse than none.** The UI would be typed against fields the server does not send.
- **Dependencies.** CLAUDE.md asks for the standard library unless it is clearly insufficient. Go has no YAML parser in it. The usual TypeScript generator, `openapi-typescript` 7.13, needs `typescript` ^5 as a peer, while TypeScript's latest is 7.0, and it pulls in six more packages.

## Decision

- **`api/openapi.json`, OpenAPI 3.1, written by hand** (the owner's choice) `[OAS-31]`.
  - Scopes are role names in each operation's security requirement: `{"bearer": ["deploy"]}`. OAS 3.1 allows that for non-OAuth schemes.
  - Nullable fields use JSON Schema type arrays (`["string", "null"]`).
  - Errors are listed where they mean something specific (404, 409, 422…), and `default` covers the rest with problem+json.
  - **Event streams** declare each event's data schema in a `x-events` extension on the `text/event-stream` media type (`message`, `end`) `[WHATWG-SSE]`.
  - `/hooks/github` is left out: it is GitHub's contract, authenticated by its signature.
- **Go tests keep it true** (`internal/api/openapi_test.go`, `encoding/json` only):
  - `TestOpenAPIRoutes`: the spec's operations and `newMux`'s routes are the same set, with the same scopes.
  - `TestOpenAPIWellFormed`: every `$ref` resolves, and operation IDs are unique.
  - **`conform`** wraps the handler in the package's tests. When a request the spec describes finishes, it checks:
    - that the status is listed;
    - that the body fits its schema, or each event's data for a stream;
    - that a request body that succeeded fits the request schema.
  - A small validator covers the JSON Schema subset the spec uses: `type`, `enum`, `const`, `required`, `properties`, `additionalProperties: false`, `items`, `pattern`, `date-time`, and ranges. `TestSchemaValidator` checks its refusals.
  - **Coverage:** a full `-tags integration` run fails if any success response in the spec was never provoked.
- **The TypeScript client (P6.1b), as the owner chose:**
  - A small Go program generates the types from `api/openapi.json`, and a Go test fails when the committed output is stale. Freshness is checked in the existing Go CI, without Node.
  - A hand-written `fetch` wrapper adds the bearer token, problem+json errors, and SSE.
  - The only npm dependency is `typescript`, a dev dependency, for `tsc`.

## Consequences

- **Positive:**
  - **Drift fails a test.** A field added, renamed or dropped on either side fails a test, and so does a route or scope that changed.
  - No new Go dependency. The spec is plain data that other tools can read.
- **Negative / risks:**
  - **JSON is verbose to edit by hand,** with no comments; descriptions carry the explanations.
  - **The checks are only as wide as the tests.** A response the tests never provoke is unchecked, except that every success status must be provoked once. Error bodies are checked against the generic problem schema.
  - The validator is a subset: a spec keyword outside it (`oneOf`, `allOf`…) would be silently ignored. Add it to the validator first.

## Implementation notes

- **2026-10-06 (P6.1b).** The decision is unchanged. As built:
  - **Generator:** `internal/openapits`. It refuses a schema keyword outside the validated subset instead of dropping it. `TestGenerated` compares its output with the committed `web/src/api/schema.ts`; `make web-types` rewrites the file.
  - **Client** (`web/src/api/client.ts`): `call(op, request)` for JSON, and `stream(op, request)`, an async generator of typed events that ends at `end`.
    - A stream resumes with `Last-Event-ID` only when the server sent a `retry` field. Operation events do; logs, whose frames have no id, do not, since a reconnect would repeat their tail.
    - The parser (`sse.ts`) runs over `fetch`, because `EventSource` cannot send an Authorization header.
  - **Checks:** `tsc` with `strict`, `exactOptionalPropertyTypes` and `erasableSyntaxOnly`. The tests run on Node 24's own TypeScript stripping and test runner `[NODE-TS]`. `@ts-expect-error` lines in the tests prove that wrong calls fail to compile.
  - **Dependencies:** `typescript` 7.0.2, exact and dev-only, with its per-platform binary packages in the lockfile `[TS-7]`. A local declaration file types `node:test`, so `@types/node` is not needed.
  - **CI:** a `web` job runs `make web-check` on Node 24.
- **2026-10-06 (P6.4).** `stream` reconnects through a server that is briefly away. A network error or a 5xx on a reconnection is retried at the server's `retry` interval, up to the same limit; a 4xx, or any error on the first connection, is final. Before, a reconnection that met the API mid-restart (the proxy's 502) ended the stream. `onOpen` reports each (re)connection.

## Alternatives considered

- **YAML:** easier to read, but a YAML library in Go's tests (a new dependency), or the checks in Node.
- **Generating the spec from Go code** by reflecting on the handler types: field names cannot drift, but it means a large generator, and descriptions inside Go. The handlers' anonymous structs would need naming.
- **`openapi-typescript`, alone or with `openapi-fetch`:** the standard tools, but TypeScript 5 pinned beside 7, more npm packages, and (with `openapi-fetch`) a runtime dependency in the UI.
