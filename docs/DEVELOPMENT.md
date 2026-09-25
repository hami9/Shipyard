# Development and Local Testing

How to build and test Shipyard on your own computer.

**Split of work:** Claude's cloud sessions run lint, unit tests, and PostgreSQL integration tests. Your local machine runs everything that needs a real Docker host (CLAUDE.md §6). When a task needs a local run, the agent names the exact section below to follow. Paste the output back so it can be recorded in the [work log](WORKLOG.md).

## 1. Choose a platform

| Your OS | Phase 0 (now) | Phase 1+ (containers, health checks, Caddy) |
| --- | --- | --- |
| **Linux** (Ubuntu 24.04 recommended), with Docker Engine | ✅ | ✅ Best match for the production VPS |
| **Windows**, via WSL2 with Ubuntu and Docker Engine installed **inside** WSL | ✅ | ✅ Run every command inside the WSL shell |
| **macOS**, or Windows with Docker Desktop | ✅ | ⚠️ Needs a Linux VM (e.g. Multipass, UTM, or a cheap VPS) |

**Why Docker Desktop falls short from Phase 1:** the worker health-checks candidate containers by their bridge-network IP (ARCHITECTURE §5, step 6). On Docker Desktop, "the Docker `bridge` network is not reachable from the host" `[DK-DESKTOP-NET]`. Phase 0 and pure-Go tests work everywhere.

## 2. Install the requirements

| Tool | Version | Check |
| --- | --- | --- |
| Go | any ≥ 1.21. `go.mod` pins toolchain **go1.26.8** and Go downloads it automatically | `go version` inside the repo prints `go1.26.8` |
| Docker Engine and the Compose plugin | Engine 29.x, Compose v2+ | `docker version`, `docker compose version` |
| make, git, curl | any recent | `make --version` |

If `go version` in the repo does not print `go1.26.8`, check that `GOTOOLCHAIN` is not set to `local` (`go env GOTOOLCHAIN` should print `auto`).

## 3. Phase 0 verification (run this now)

```bash
git clone https://github.com/hami9/Shipyard.git shipyard
cd shipyard
git checkout claude/shipyard-architecture-proposal-j8w56b

go version                 # expect go1.26.8
make lint                  # gofmt, go vet, staticcheck
make test                  # unit tests with -race
make build && ./bin/shipyard-api version

make dev-up                # PostgreSQL 18 on 127.0.0.1:54320 (Docker)
make migrate               # expect: "migration applied" ... version=1
make migrate               # expect: applied=0 (idempotent)
make test-integration      # expect: every package "ok"

# Run the API and probe it (second terminal for curl)
make run-api
curl -i http://127.0.0.1:8080/healthz    # 200 {"status":"ok"} + X-Request-Id header
curl -i http://127.0.0.1:8080/readyz     # 200 {"status":"ready"}
# Stop the API with Ctrl+C. Expect "api shutting down" and a clean exit.

make dev-down              # stop PostgreSQL (data kept; `make dev-reset` deletes it)
```

**Send back:** your OS and version, plus the last lines of `make lint`, `make test`, and `make test-integration`, and the two `curl` responses.

## 4. Everyday commands

Run `make help` for the full list.

| Command | What it does |
| --- | --- |
| `make fmt` | Format Go code |
| `make lint test` | Required before every commit |
| `make test-integration` | Needs `make dev-up`. Uses throwaway databases and drops them afterwards |
| `make run-api` / `make run-worker` | Run against the dev database with text logs |
| `make dev-reset` | Wipe the dev database volume |

Configuration comes only from `SHIPYARD_*` environment variables. See [deploy/shipyard.env.example](../deploy/shipyard.env.example). The Makefile supplies dev defaults, and you can override any of them, e.g. `make migrate SHIPYARD_DATABASE_URL=...`.

## 5. Troubleshooting

| Symptom | Fix |
| --- | --- |
| `port is already allocated` on `make dev-up` | Something already uses 54320. Stop it, or run `make dev-up` after editing the port in `deploy/dev/compose.yaml` and the two URLs in the Makefile |
| `SHIPYARD_TEST_DATABASE_URL is not set` | Run the tests through `make test-integration`, or export the variable yourself |
| `go: downloading go1.26.8` hangs | The Go proxy is unreachable. Set `GOPROXY=https://proxy.golang.org,direct` and check the network |
| staticcheck errors mentioning Go 1.27 | You are building with a newer local toolchain. Keep `GOTOOLCHAIN=auto` so `go.mod`'s pin applies |
