#!/bin/bash
# The UI tests' API side, on Linux, for a Playwright run on another host:
# Windows with Docker in WSL, where WSL forwards the API's port to Windows
# but not the dev database's, which Docker publishes by iptables alone.
#
#   web/e2e/serve-api.sh [PORT] [OUT]   seed a database, run shipyard-api on
#                                       127.0.0.1:PORT (default 18090), write
#                                       OUT (default web/e2e/.external.json)
#
# Then, on the other host: SHIPYARD_UI_EXTERNAL=<OUT> npx playwright test.
# Ctrl-C stops the API and drops the database. Logs from a worker are not
# served in this mode: the logs page meets "the worker is not running".
set -euo pipefail
repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo"
port=${1:-18090}
out=${2:-web/e2e/.external.json}
export SHIPYARD_TEST_DATABASE_URL=${SHIPYARD_TEST_DATABASE_URL:-postgres://shipyard:shipyard@127.0.0.1:54320/postgres?sslmode=disable}

tmp=$(mktemp -d)
seed=$(go run ./test/uiseed create "$tmp/kek")
field() { printf '%s' "$seed" | sed -n "s/.*\"$1\":\"\\([^\"]*\\)\".*/\\1/p"; }
db=$(field database)
cleanup() {
	[ -n "${api:-}" ] && kill "$api" 2>/dev/null && wait "$api" 2>/dev/null
	go run ./test/uiseed drop "$db"
	rm -rf "$tmp" "$out"
}
trap cleanup EXIT

go build -o "$tmp/shipyard-api" ./cmd/shipyard-api
SHIPYARD_DATABASE_URL=$(field database_url) SHIPYARD_API_LISTEN=127.0.0.1:$port SHIPYARD_KEK_DIR="$tmp/kek" \
	SHIPYARD_KEK_ACTIVE=$(field kek_id) SHIPYARD_WORKER_SOCKET="$tmp/logs.sock" SHIPYARD_DNS_PREFLIGHT=false \
	SHIPYARD_API_RATE=0 SHIPYARD_AUTH_FAILURES=0 SHIPYARD_LOG_FORMAT=text "$tmp/shipyard-api" serve &
api=$!
for _ in $(seq 120); do curl -fsS "http://127.0.0.1:$port/healthz" > /dev/null 2>&1 && break; sleep 0.25; done
# The seed's JSON, with where the API is (umask: it holds test tokens).
(umask 077 && printf '%s' "$seed" | sed "s|^{|{\"api_url\":\"http://127.0.0.1:$port\",|" > "$out")
echo "API on http://127.0.0.1:$port; $out written. Ctrl-C to stop and drop $db."
wait "$api"
