#!/bin/bash
# Host firewall for Shipyard's build networks (ADR-0009, P5.7a).
#
# Builds run on bridges named sybuild-* (internal/build.BridgePrefix). This
# keeps them away from:
#   - the host's own services (anything it listens on, on any address):
#     traffic from a container to the host goes through INPUT;
#   - cloud metadata and other link-local addresses (169.254.0.0/16, and
#     AWS's fd00:ec2::254): forwarded traffic, filtered in DOCKER-USER, which
#     Docker evaluates before its own rules [DK-IPTABLES].
# Internet access stays open: builds fetch dependencies (ADR-0009).
#
# Usage: shipyard-firewall.sh [apply|remove|status]   (as root)
# apply is idempotent: it refills Shipyard's own chains and adds each jump
# once. shipyard-firewall.service runs it after Docker starts.
set -euo pipefail

readonly IFACE='sybuild-+'
readonly IN=SHIPYARD-BUILD-IN
readonly FWD=SHIPYARD-BUILD-FWD
# Tests point these at stubs; production uses the system's.
IPTABLES=${SHIPYARD_IPTABLES:-iptables}
IP6TABLES=${SHIPYARD_IP6TABLES:-ip6tables}
DOCKER=${SHIPYARD_DOCKER:-docker}

die() {
	echo "shipyard-firewall: $*" >&2
	exit 1
}

# chain_exists TOOL CHAIN
chain_exists() { "$1" -w -n -L "$2" >/dev/null 2>&1; }

# jump TOOL FROM TO: FROM's first rule sends build traffic to TO, once.
jump() {
	"$1" -w -C "$2" -i "$IFACE" -j "$3" 2>/dev/null || "$1" -w -I "$2" 1 -i "$IFACE" -j "$3"
}

# unjump TOOL FROM TO: removes every such jump.
unjump() {
	while "$1" -w -C "$2" -i "$IFACE" -j "$3" 2>/dev/null; do
		"$1" -w -D "$2" -i "$IFACE" -j "$3"
	done
}

# fill TOOL METADATA...: Shipyard's chains for one address family.
fill() {
	local tool=$1
	shift
	chain_exists "$tool" "$IN" || "$tool" -w -N "$IN"
	"$tool" -w -F "$IN"
	# Replies to what the host itself opened stay allowed.
	"$tool" -w -A "$IN" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
	"$tool" -w -A "$IN" -j DROP
	chain_exists "$tool" "$FWD" || "$tool" -w -N "$FWD"
	"$tool" -w -F "$FWD"
	local dst
	for dst in "$@"; do
		"$tool" -w -A "$FWD" -d "$dst" -j DROP
	done
	"$tool" -w -A "$FWD" -j RETURN
	jump "$tool" INPUT "$IN"
	jump "$tool" DOCKER-USER "$FWD"
}

clear_family() {
	local tool=$1
	unjump "$tool" INPUT "$IN"
	chain_exists "$tool" DOCKER-USER && unjump "$tool" DOCKER-USER "$FWD"
	local c
	for c in "$IN" "$FWD"; do
		if chain_exists "$tool" "$c"; then
			"$tool" -w -F "$c"
			"$tool" -w -X "$c"
		fi
	done
}

has_ip6() { command -v "$IP6TABLES" >/dev/null 2>&1 && chain_exists "$IP6TABLES" DOCKER-USER; }

apply() {
	# Docker's nftables backend (experimental in 29.x) has no DOCKER-USER
	# chain [DK-NFTABLES]; these rules are for the default iptables one.
	local backend
	backend=$("$DOCKER" info --format '{{.FirewallBackend.Driver}}' 2>/dev/null) || die "docker info failed: is Docker running?"
	case "$backend" in
	iptables | "") ;;
	*) die "Docker uses the $backend firewall backend, which has no DOCKER-USER chain; these rules need the iptables backend" ;;
	esac
	chain_exists "$IPTABLES" DOCKER-USER || die "no DOCKER-USER chain: start Docker first"
	fill "$IPTABLES" 169.254.0.0/16
	if has_ip6; then
		fill "$IP6TABLES" fd00:ec2::254/128
	fi
	echo "shipyard-firewall: build networks ($IFACE) cannot reach the host or link-local addresses"
}

remove() {
	clear_family "$IPTABLES"
	if command -v "$IP6TABLES" >/dev/null 2>&1; then
		clear_family "$IP6TABLES"
	fi
	echo "shipyard-firewall: rules removed"
}

status() {
	local tool c
	for tool in "$IPTABLES" "$IP6TABLES"; do
		command -v "$tool" >/dev/null 2>&1 || continue
		for c in "$IN" "$FWD"; do
			chain_exists "$tool" "$c" && "$tool" -w -S "$c"
		done
		"$tool" -w -S INPUT | grep -F -- "-j $IN" || true
		chain_exists "$tool" DOCKER-USER && { "$tool" -w -S DOCKER-USER | grep -F -- "-j $FWD" || true; }
	done
}

case "${1:-apply}" in
apply) apply ;;
remove) remove ;;
status) status ;;
*) die "usage: $0 [apply|remove|status]" ;;
esac
