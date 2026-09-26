# Development and Local Testing

How to build and test Shipyard on your own computer.

**Split of work:** Claude's cloud sessions run lint, unit tests, and PostgreSQL integration tests. Your local machine runs everything that needs a real Docker host (CLAUDE.md §6). When a task needs a local run, the agent names the exact section below to follow. Paste the output back so it can be recorded in the [work log](WORKLOG.md).

## 1. Choose a platform

| Your OS | Phase 0 (now) | Phase 1+ (containers, health checks, Caddy) |
| --- | --- | --- |
| **Linux** (Ubuntu 24.04 recommended), with Docker Engine | ✅ | ✅ Best match for the production VPS |
| **Windows**, via WSL2 with Ubuntu and Docker Engine installed **inside** WSL (**owner setup**, §2) | ✅ | ✅ Run every command inside the WSL shell |
| **macOS**, or Windows with Docker Desktop | ✅ | ⚠️ Needs a Linux VM (e.g. Multipass, UTM, or a cheap VPS) |

**Why Docker Desktop falls short from Phase 1:** the worker health-checks candidate containers by their bridge-network IP (ARCHITECTURE §5, step 6). On Docker Desktop, "the Docker `bridge` network is not reachable from the host" `[DK-DESKTOP-NET]`. Phase 0 and pure-Go tests work everywhere.

## 2. Setup on Windows with WSL2 (the owner's platform)

Everything runs **inside Ubuntu on WSL2**: Go, Docker Engine, and the repository. Do not install Docker Desktop, and do not work from `/mnt/c`. Keep the code in the Linux file system (`~/…`) for speed `[MS-WSL-FS]`.

### 2.1 Check WSL (in PowerShell)

```powershell
wsl --version      # errors or a very old version? run: wsl --update
wsl -l -v          # your Ubuntu distro must show VERSION 2
```

WSL 1 cannot run Docker Engine. If needed, convert with `wsl --set-version <distro> 2`.

### 2.2 Check systemd (inside Ubuntu)

Docker Engine runs as a systemd service. Current Ubuntu images installed with `wsl --install` already use systemd `[MS-WSL-SYSTEMD]`. Check with:

```bash
systemctl is-system-running   # "running" or "degraded" = OK; an error = systemd is off
```

If it is off, add these two lines to `/etc/wsl.conf` (`sudo nano /etc/wsl.conf`):

```ini
[boot]
systemd=true
```

Then run `wsl --shutdown` in PowerShell and reopen Ubuntu.

### 2.3 Install Docker Engine (inside Ubuntu)

These are the official apt repository steps `[DK-INSTALL-UBUNTU]`:

```bash
sudo apt update
sudo apt install -y ca-certificates curl build-essential git
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
sudo tee /etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"   # dev machine only: docker group = root-equivalent [DK-POSTINSTALL]
```

Close and reopen the Ubuntu terminal so the group applies. `build-essential` provides `make` and `gcc`. The race detector used by `make test` needs `gcc`.

### 2.4 Install Go (inside Ubuntu)

Install the exact toolchain that `go.mod` pins, so no automatic toolchain download is needed `[GO-INSTALL]`:

```bash
cd /tmp
curl -fsSLO https://go.dev/dl/go1.26.8.linux-amd64.tar.gz
echo "d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b  go1.26.8.linux-amd64.tar.gz" | sha256sum -c
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.26.8.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.profile
source ~/.profile
go version   # go1.26.8 linux/amd64
```

On an ARM Windows PC (`uname -m` prints `aarch64`), use `go1.26.8.linux-arm64.tar.gz` and its checksum from https://go.dev/dl/ instead.

### 2.5 Sanity checks

These confirm what Phase 1+ depends on:

```bash
docker info --format '{{.OperatingSystem}}'   # must be "Ubuntu …", NOT "Docker Desktop"
docker compose version

# The host must reach a container by its bridge IP (worker health checks, ARCHITECTURE §5)
docker run -d --rm --name probe-test nginx:alpine
IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' probe-test)
curl -s -o /dev/null -w "HTTP %{http_code}\n" "http://$IP/"   # expect: HTTP 200
docker stop probe-test
```

### Other platforms

On native Linux, start at 2.3. On macOS, use a Linux VM for Phase 1+.

| Tool | Version | Check |
| --- | --- | --- |
| Go | go1.26.8 (or any ≥ 1.21, which then downloads go1.26.8 automatically) | `go version` inside the repo |
| Docker Engine and the Compose plugin | Engine 29.x, Compose v2+ | `docker version`, `docker compose version` |
| make, gcc, git, curl | any recent | `make --version` |

If `go version` in the repo does not print `go1.26.8`, check that `GOTOOLCHAIN` is not set to `local` (`go env GOTOOLCHAIN` should print `auto`).

## 3. Phase 0 verification (run this now)

```bash
cd ~
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

**Send back:** the output of §2.5, the last lines of `make lint`, `make test`, and `make test-integration`, and the two `curl` responses.

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
| Requests to `127.0.0.1:8080` reach another service, or `make run-api` fails with `address already in use` | Another program uses 8080. On WSL, containers published by Docker Desktop count too, because all distros share one network. Run with `SHIPYARD_API_LISTEN=127.0.0.1:18080` |
| `SHIPYARD_TEST_DATABASE_URL is not set` | Run the tests through `make test-integration`, or export the variable yourself |
| `go: downloading go1.26.8` hangs | The Go proxy is unreachable. Set `GOPROXY=https://proxy.golang.org,direct` and check the network |
| staticcheck errors mentioning Go 1.27 | You are building with a newer local toolchain. Keep `GOTOOLCHAIN=auto` so `go.mod`'s pin applies |
| WSL: `apt` hangs or `curl` fails with "Could not resolve host", but `ping 1.1.1.1` works | WSL's DNS proxy fails while a Windows VPN is connected. In the distro, add `[network]` / `generateResolvConf=false` to `/etc/wsl.conf`, run `wsl --terminate <distro>`, then replace `/etc/resolv.conf` (a symlink) with a file containing `nameserver 1.1.1.1` `[MS-WSL-CONF]` |
| WSL: `apt` prints `Ign:` for every package | IPv6 does not route. Run `echo 'Acquire::ForceIPv4 "true";' \| sudo tee /etc/apt/apt.conf.d/99force-ipv4` |
| `docker` inside Ubuntu is `/mnt/c/Program Files/Docker/…` | That is Docker Desktop's Windows CLI leaking in through the Windows PATH. Install Docker Engine (§2.3) and keep Docker Desktop's WSL integration **off** for this distro |
