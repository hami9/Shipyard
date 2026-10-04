# ADR-0009: Builder egress control deferred; the builder gets its own network

- **Status:** Accepted
- **Date:** 2026-10-04
- **Deciders:** Project owner (option chosen 2026-10-04), Claude Code (evaluation)
- **Sources:** `DK-BX-CONTAINER`, `DK-PREDEF-ARGS`, `DK-NET-INTERNAL`, `DK-26-DNS`, `BK-PROXY-NETWORK`

## Context

- Builds run repository code (`RUN` steps) with unrestricted network access (ADR-0004). A malicious Dockerfile could exfiltrate what the build can see, attack other hosts, or reach the cloud metadata service. P5.1 asked for a proxy or allow-list, or an explicit deferral.
- The MVP deploys only repositories the single admin registers, and is not a sandbox for untrusted code (ADR-0007). Builds see no Shipyard credentials (ADR-0004, invariant 8).
- Until now the builder container sat on Docker's default bridge, together with any other container of the host that uses it.
- What an allow-list would take (evaluated 2026-10-04):
  - **Cutting the builder off.** The buildx `docker-container` driver takes `network=<name>` and `env.<key>` driver options `[DK-BX-CONTAINER]`. An `--internal` network has no default route, and firewall rules block traffic in and out, but the host's gateway IP stays reachable `[DK-NET-INTERNAL]`. Since Engine 26.0, a container only on internal networks gets no external DNS `[DK-26-DNS]`.
  - **A proxy.** Builds would then reach the internet only through a forward proxy with a host allow-list, via `HTTP_PROXY`/`HTTPS_PROXY`. These are predefined build args, need no `ARG`, and are kept out of `docker history` `[DK-PREDEF-ARGS]`. buildkitd needs the proxy too, for base images (`env.*`).
  - **BuildKit's own proxy.** BuildKit v0.33.1 has `--proxy-network` ("proxy network enforcement for all builds") with source-policy allow rules `[BK-PROXY-NETWORK]`. It is new, documented mainly in issues, an explicit proxy (tools that ignore the variables are not covered), and has known port and CA quirks.
  - **Either way:** a pinned third-party proxy image or an experimental BuildKit mode; an allow-list per ecosystem (registries, package mirrors, git hosts) that operators must tune; builds that fail until they do; and about 500 lines with Docker tests.

## Decision

- **Defer builder egress control.** Builds keep outbound network access, as in ADR-0004, under the trusted-repositories model of ADR-0007.
- **Isolate the builder on the host now:**
  - The builder's container joins a bridge network of its own, `<builder>-build` (default `shipyard-build`), labelled `io.shipyard.role=build`, instead of Docker's default bridge.
  - Nothing else is on that network, so a build cannot reach other containers by address.
  - The worker creates the network with the builder. `Builder.Remove` deletes both.
  - A builder created before this change stays on the default bridge until it is removed (`docker buildx rm shipyard`; its cache goes with it) and the worker recreates it. (Since ADR-0010 the worker replaces such a builder itself, because its image differs.)
- **Block what the bridge does not (P5.7).** The installer adds host firewall rules for the build network's subnet, found by its label:
  - deny the link-local metadata address `169.254.169.254`;
  - deny the host's own services except what builds need (none by default).
  - Until P5.7, the operator guide says to do this by hand.
- **Revisit when** any of these happens:
  - Shipyard accepts repositories the admin does not control: multi-user (P7.1), or webhook-registered repositories.
  - An app needs builds without internet access.
  - BuildKit's proxy network becomes documented and stable.
  - A security review (P5.8) finds the residual risk unacceptable.

## Consequences

- **Positive:**
  - Builds keep working with no allow-list to maintain.
  - The builder no longer shares a network with unrelated containers.
  - Firewalling it needs only its label.
  - Small change, no new dependency.
- **Negative / risks:**
  - A malicious or compromised Dockerfile in a registered repository can still send data out and reach the internet during its build.
  - Until P5.7's rules are in place, it can reach the cloud metadata service and host services listening on the bridge gateway.
  - The roadmap's risk register keeps this risk, with this ADR as its record.
- **Follow-ups:**
  - P5.7: the firewall rules for the build network, and the operator guide.
  - P5.8: review this residual risk.
  - P7: revisit with multi-user.

## Alternatives considered

- **Allow-list forward proxy on an internal network:** the strongest, but a new third-party image, an allow-list to keep per ecosystem, and broken builds until it is tuned. It pays off once repositories are not trusted, which the MVP rules out.
- **BuildKit `--proxy-network` with source policies:** no extra container, but too new and too thinly documented to rely on yet. It also covers only tools that honor proxy variables.
- **No network for builds:** most builds download dependencies, and buildkitd itself pulls base images. Unusable as a default.
