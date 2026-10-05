#!/bin/bash
# Installs or upgrades Shipyard on one Ubuntu or Debian host (P5.7b).
#
# Run it as root from an extracted shipyard-server release (or a checkout
# after `make build`). It installs Shipyard's binaries from there and never
# downloads Shipyard. When missing, it installs Docker Engine and
# PostgreSQL 18 from their vendors' apt repositories [DK-INSTALL][PG-APT].
#
# Safe to run again: an upgrade installs the new binaries and units, runs the
# migrations, and restarts the services; it never touches the database's
# data, the KEKs, or an existing shipyard.env. Back up first
# (docs/OPERATIONS.md).
set -euo pipefail

usage() {
	cat <<'EOF'
Usage: sudo deploy/install.sh [options]

  --bin DIR           where shipyard-api and shipyard-worker are
                      (default: the release directory, or ./bin)
  --public-ip IP      this server's public IP, for the DNS preflight of new
                      domains (SHIPYARD_PUBLIC_IPS); repeat for IPv4 and IPv6
  --api-hostname H    publish the API at https://H (SHIPYARD_API_HOSTNAME)
  --acme-email E      contact address for Let's Encrypt (SHIPYARD_ACME_EMAIL)
  --skip-build-check  do not try a rootless build after the install
  --dry-run           print what would change, change nothing
  -h, --help          this help

The first three only apply to a new /etc/shipyard/shipyard.env.
EOF
}

# --- settings ---------------------------------------------------------------

readonly ETC=/etc/shipyard
readonly ENV_FILE=$ETC/shipyard.env
readonly KEK_DIR=$ETC/kek
readonly KEK_ID=k1
readonly WORK_DIR=/var/lib/shipyard/work
readonly BACKUP_DIR=/var/backups/shipyard
readonly BACKUP_KEK_DIR=/var/backups/shipyard-kek
readonly LIB=/usr/local/lib/shipyard
readonly BIN=/usr/local/bin
readonly UNITS=/etc/systemd/system
readonly PG_MAJOR=18
readonly DOCKER_MIN_MAJOR=29

DRY_RUN=0
BUILD_CHECK=1
BIN_DIR=
PUBLIC_IPS=
API_HOSTNAME=
ACME_EMAIL=
FIRST_INSTALL=0

DEPLOY=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly DEPLOY

say() { printf '==> %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

# run prints a command and runs it, or only prints it in a dry run.
run() {
	printf '  + %s\n' "$*"
	[ "$DRY_RUN" = 1 ] || "$@"
}

# put SRC DEST MODE OWNER: installs a file, reporting whether it changed.
put() {
	if [ -f "$2" ] && cmp -s "$1" "$2"; then
		return 0
	fi
	run install -D -m "$3" -o "${4%:*}" -g "${4#*:}" "$1" "$2"
}

# --- arguments --------------------------------------------------------------

while [ $# -gt 0 ]; do
	case "$1" in
	--bin) BIN_DIR=${2:?--bin needs a directory}; shift 2 ;;
	--public-ip) PUBLIC_IPS=${PUBLIC_IPS:+$PUBLIC_IPS,}${2:?--public-ip needs an address}; shift 2 ;;
	--api-hostname) API_HOSTNAME=${2:?--api-hostname needs a hostname}; shift 2 ;;
	--acme-email) ACME_EMAIL=${2:?--acme-email needs an address}; shift 2 ;;
	--skip-build-check) BUILD_CHECK=0; shift ;;
	--dry-run) DRY_RUN=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "unknown option $1" ;;
	esac
done

if [ -z "$BIN_DIR" ]; then
	if [ -x "$DEPLOY/../shipyard-api" ]; then
		BIN_DIR=$DEPLOY/.. # a release archive: binaries next to deploy/
	else
		BIN_DIR=$DEPLOY/../bin # a checkout after make build
	fi
fi
for b in shipyard-api shipyard-worker; do
	[ -x "$BIN_DIR/$b" ] || die "no $b in $BIN_DIR (use --bin, or run make build)"
done

# --- checks -----------------------------------------------------------------

[ "$DRY_RUN" = 1 ] || [ "$(id -u)" = 0 ] || die "run as root (sudo), or with --dry-run"
[ -r /etc/os-release ] || die "no /etc/os-release: Ubuntu or Debian only"
# shellcheck disable=SC1091
. /etc/os-release
case "${ID:-}" in
ubuntu | debian) ;;
*) die "unsupported OS ${ID:-unknown}: Ubuntu or Debian only" ;;
esac
CODENAME=${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}
[ -n "$CODENAME" ] || die "cannot tell the release codename from /etc/os-release"
command -v systemctl >/dev/null || die "systemd is required"
ARCH=$(dpkg --print-architecture)
case "$ARCH" in
amd64 | arm64) ;;
*) die "unsupported architecture $ARCH: amd64 or arm64" ;;
esac
[ "$DRY_RUN" = 1 ] && say "dry run: nothing is changed"

# --- packages ---------------------------------------------------------------

apt_install() {
	run env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"
}

say "base packages"
missing=()
for p in ca-certificates curl git; do
	dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q "ok installed" || missing+=("$p")
done
if [ ${#missing[@]} -gt 0 ]; then
	run apt-get update
	apt_install "${missing[@]}"
fi

# add_repo NAME KEY_URL KEY_PATH URI SUITE COMPONENTS: an apt source in deb822 format,
# signed by the vendor's key [DK-INSTALL][PG-APT].
add_repo() {
	local name=$1 key_url=$2 key=$3 uri=$4 suite=$5 components=$6 src
	run install -m 0755 -d "$(dirname "$key")"
	run curl -fsSL "$key_url" -o "$key"
	run chmod a+r "$key"
	src=$(mktemp)
	printf 'Types: deb\nURIs: %s\nSuites: %s\nComponents: %s\nArchitectures: %s\nSigned-By: %s\n' \
		"$uri" "$suite" "$components" "$ARCH" "$key" >"$src"
	put "$src" "/etc/apt/sources.list.d/$name.sources" 0644 root:root
	rm -f "$src"
}

say "Docker Engine"
if ! command -v docker >/dev/null; then
	add_repo docker "https://download.docker.com/linux/$ID/gpg" /etc/apt/keyrings/docker.asc \
		"https://download.docker.com/linux/$ID" "$CODENAME" stable
	run apt-get update
	apt_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin
else
	v=$(docker version --format '{{.Server.Version}}' 2>/dev/null || true)
	[ -n "$v" ] || die "docker is installed but its daemon does not answer"
	[ "${v%%.*}" -ge "$DOCKER_MIN_MAJOR" ] 2>/dev/null || warn "Docker Engine $v: Shipyard is tested with $DOCKER_MIN_MAJOR.x"
	docker buildx version >/dev/null 2>&1 || apt_install docker-buildx-plugin
fi
# The local log driver and live-restore [DK-LOG][DK-LIVE]. An existing
# daemon.json is the operator's: report a difference, never overwrite it.
if [ ! -f /etc/docker/daemon.json ]; then
	put "$DEPLOY/docker/daemon.json" /etc/docker/daemon.json 0644 root:root
	run systemctl restart docker
elif ! cmp -s "$DEPLOY/docker/daemon.json" /etc/docker/daemon.json; then
	warn "/etc/docker/daemon.json differs from deploy/docker/daemon.json: merge log-driver \"local\" and live-restore by hand"
fi
run systemctl enable --now docker

say "PostgreSQL $PG_MAJOR"
if ! dpkg-query -W -f='${Status}' "postgresql-$PG_MAJOR" 2>/dev/null | grep -q "ok installed"; then
	add_repo pgdg https://www.postgresql.org/media/keys/ACCC4CF8.asc \
		/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
		https://apt.postgresql.org/pub/repos/apt "$CODENAME-pgdg" main
	run apt-get update
	apt_install "postgresql-$PG_MAJOR"
fi
run systemctl enable --now postgresql

# --- users and directories --------------------------------------------------

say "users and directories"
getent group shipyard >/dev/null || run groupadd --system shipyard
# Homes under /var/lib: the units hide /home (ProtectHome=yes), and the
# worker's StateDirectory= creates its home, where buildx keeps its state.
getent passwd shipyard-api >/dev/null ||
	run useradd --system --gid shipyard --home-dir /var/lib/shipyard-api --no-create-home --shell /usr/sbin/nologin shipyard-api
getent passwd shipyard-worker >/dev/null ||
	run useradd --system --gid shipyard --groups docker --home-dir /var/lib/shipyard-worker --no-create-home --shell /usr/sbin/nologin shipyard-worker
id -nG shipyard-worker 2>/dev/null | grep -qw docker || run usermod -aG docker shipyard-worker

run install -d -m 0750 -o root -g shipyard "$ETC" "$KEK_DIR"
run install -d -m 0755 -o root -g root /var/lib/shipyard "$LIB"
run install -d -m 0700 -o shipyard-worker -g shipyard /var/lib/shipyard-worker "$WORK_DIR" "$BACKUP_DIR" "$BACKUP_KEK_DIR"

# --- files ------------------------------------------------------------------

say "binaries and units"
for b in shipyard-api shipyard-worker shipyard; do
	[ -x "$BIN_DIR/$b" ] && put "$BIN_DIR/$b" "$BIN/$b" 0755 root:root
done
put "$DEPLOY/firewall/shipyard-firewall.sh" "$LIB/shipyard-firewall.sh" 0755 root:root
for u in shipyard-api.service shipyard-worker.service shipyard-firewall.service shipyard-backup.service shipyard-backup.timer; do
	put "$DEPLOY/systemd/$u" "$UNITS/$u" 0644 root:root
done
run systemctl daemon-reload

# Ubuntu 24.04+ restricts unprivileged user namespaces, which rootless
# BuildKit needs (ADR-0010) [UB-USERNS].
if [ "$ID" = ubuntu ] && dpkg --compare-versions "${VERSION_ID:-0}" ge 24.04; then
	say "user namespaces for rootless BuildKit"
	put "$DEPLOY/sysctl/60-shipyard-buildkit.conf" /etc/sysctl.d/60-shipyard-buildkit.conf 0644 root:root
	run sysctl --system --quiet
fi

say "build network firewall (ADR-0009)"
run systemctl enable --now shipyard-firewall.service

# --- configuration, database, KEK -------------------------------------------

if [ ! -f "$ENV_FILE" ]; then
	FIRST_INSTALL=1
	say "configuration and database (first install)"
	password=$(head -c 32 /dev/urandom | sha256sum | cut -c1-40) # hex: safe in a URL and in SQL
	env_new=$(mktemp)
	sed -e "s|^SHIPYARD_DATABASE_URL=.*|SHIPYARD_DATABASE_URL=postgres://shipyard:$password@127.0.0.1:5432/shipyard?sslmode=disable|" \
		-e "s|^SHIPYARD_PUBLIC_IPS=.*|SHIPYARD_PUBLIC_IPS=$PUBLIC_IPS|" \
		-e "s|^SHIPYARD_API_HOSTNAME=.*|SHIPYARD_API_HOSTNAME=$API_HOSTNAME|" \
		-e "s|^SHIPYARD_KEK_ACTIVE=.*|SHIPYARD_KEK_ACTIVE=$KEK_ID|" \
		"$DEPLOY/shipyard.env.example" >"$env_new"
	[ -z "$ACME_EMAIL" ] || printf '\nSHIPYARD_ACME_EMAIL=%s\n' "$ACME_EMAIL" >>"$env_new"
	# The role and database, created or given this password; the SQL goes
	# on stdin, so the password is never on a command line.
	sql="DO \$\$ BEGIN
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'shipyard') THEN
    ALTER ROLE shipyard LOGIN PASSWORD '$password';
  ELSE
    CREATE ROLE shipyard LOGIN PASSWORD '$password';
  END IF;
END \$\$;
SELECT 'CREATE DATABASE shipyard OWNER shipyard' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'shipyard')\\gexec"
	printf '  + psql (create role and database shipyard)\n'
	[ "$DRY_RUN" = 1 ] || printf '%s\n' "$sql" | runuser -u postgres -- psql -q -v ON_ERROR_STOP=1 -f -
	put "$env_new" "$ENV_FILE" 0640 root:shipyard
	rm -f "$env_new"
	unset password sql
else
	say "keeping $ENV_FILE"
fi

if ! compgen -G "$KEK_DIR/*.key" >/dev/null && ! compgen -G "$KEK_DIR/*.hpke" >/dev/null; then
	# An HPKE pair: the API seals with the public key and cannot decrypt
	# (ADR-0012). The private key is the worker's alone.
	say "KEK $KEK_ID (HPKE)"
	run env SHIPYARD_KEK_DIR="$KEK_DIR" "$BIN/shipyard-worker" kek generate "$KEK_ID"
	run chown shipyard-worker:shipyard "$KEK_DIR/$KEK_ID.hpke"
	run chown root:shipyard "$KEK_DIR/$KEK_ID.pub"
	warn "back up $KEK_DIR off this host now: without it, secrets cannot be decrypted (ADR-0005)"
fi

# as_user USER CMD...: runs a Shipyard command with shipyard.env, as USER.
as_user() {
	local user=$1
	shift
	if [ "$DRY_RUN" = 1 ]; then
		printf '  + (as %s, with %s) %s\n' "$user" "$ENV_FILE" "$*"
		return 0
	fi
	(
		set -a
		# shellcheck disable=SC1090
		. "$ENV_FILE"
		set +a
		runuser -u "$user" -- "$@"
	)
}

say "database migrations"
as_user shipyard-api "$BIN/shipyard-api" migrate

# --- services ---------------------------------------------------------------

say "services"
if [ "$FIRST_INSTALL" = 1 ]; then
	run systemctl enable --now shipyard-api.service shipyard-worker.service shipyard-backup.timer
else
	run systemctl enable shipyard-api.service shipyard-worker.service shipyard-backup.timer
	run systemctl restart shipyard-api.service shipyard-worker.service
fi

# ADR-0010: the worker creates the rootless builder at start; one build with
# a RUN step proves that user namespaces work on this kernel.
if [ "$BUILD_CHECK" = 1 ] && [ "$DRY_RUN" = 0 ]; then
	say "rootless build check (pulls busybox once)"
	builder=$(sed -n 's/^SHIPYARD_BUILDER=//p' "$ENV_FILE")
	builder=${builder:-shipyard}
	worker() { runuser -u shipyard-worker -- env HOME=/var/lib/shipyard-worker "$@"; }
	for _ in $(seq 60); do
		worker docker buildx inspect "$builder" >/dev/null 2>&1 && break
		sleep 5
	done
	if printf 'FROM busybox:1.37\nRUN id -u && touch /ok\n' |
		worker docker buildx build --builder "$builder" --no-cache --progress plain - >/tmp/shipyard-build-check.log 2>&1; then
		say "a rootless build works"
	else
		warn "the build check failed: see /tmp/shipyard-build-check.log and docs/OPERATIONS.md (Troubleshooting)"
	fi
fi

# --- next steps -------------------------------------------------------------

if [ "$FIRST_INSTALL" = 1 ] && [ "$DRY_RUN" = 0 ]; then
	say "an admin token for the CLI (shown once)"
	as_user shipyard-api "$BIN/shipyard-api" token create --name admin
fi

if [ "$DRY_RUN" = 1 ]; then
	printf '\nDry run finished: nothing was changed.\n'
	exit 0
fi
cat <<EOF

Shipyard is installed. Next:
  - Edit $ENV_FILE as needed (SHIPYARD_PUBLIC_IPS is required to add
    domains while the DNS preflight is on), then restart both services.
  - Copy $KEK_DIR off this host, and set SHIPYARD_BACKUP_HOOK and
    SHIPYARD_BACKUP_KEK_HOOK so nightly backups leave it too.
  - On a workstation: shipyard login --url https://<api hostname> (paste
    the token), then shipyard app create.
  - Operator guide: docs/OPERATIONS.md
EOF
