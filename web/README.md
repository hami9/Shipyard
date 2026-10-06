# web

Shipyard's web UI, Phase 6 of the [roadmap](../docs/ROADMAP.md). It is never required for a deploy: the CLI does everything.

Today it holds the typed API client ([ADR-0015](../docs/adr/0015-openapi-and-typescript-client.md)):

| File | What |
| --- | --- |
| `src/api/schema.ts` | **Generated** from [api/openapi.json](../api/openapi.json) by `internal/openapits`. Do not edit: run `make web-types` |
| `src/api/client.ts` | `Client`: `call` for JSON operations, `stream` for event streams (resumed with `Last-Event-ID`), `ApiError` for problem details |
| `src/api/sse.ts` | A server-sent events parser over `fetch`, which, unlike `EventSource`, can send the bearer token |

```ts
const api = new Client({ baseUrl: "https://shipyard.example.com", token });
const { apps } = await api.call("listApps");
for await (const e of api.stream("streamEvents", { path: { id } })) {
  if (e.event === "end") console.log(e.data.status);
}
```

## Checks

Node.js 24 or newer. `typescript` is the only dependency, and only for development; Node runs the tests' TypeScript directly.

```bash
make web-check
```

It runs `npm ci`, `tsc` (types, including the `@ts-expect-error` lines in the tests), and `node --test`. That the generated file is current is a Go test, `go test ./internal/openapits`, run by `make test`.
