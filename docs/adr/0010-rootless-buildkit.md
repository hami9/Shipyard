# ADR-0010: Rootless BuildKit for builds; the Docker daemon stays rootful

- **Status:** Accepted
- **Date:** 2026-10-04
- **Deciders:** Project owner (option chosen 2026-10-04), Claude Code (evaluation)
- **Sources:** `BK-ROOTLESS`, `BX-PRIVILEGED`, `UB-USERNS`, `SYSTEMD-EXEC`, `DK-SECCOMP`, `DK-ROOTLESS`, `DK-USERNS`, `DK-CONTAINERD`, `GVISOR-DOCKER`

## Context

- **Builds run repository code.** Every deploy runs the repository's `RUN` steps, and with them its dependencies' install scripts (ADR-0004). The repositories are trusted (ADR-0007), but their dependencies are a supply chain.
- **How builds ran until now.**
  - buildx's `docker-container` driver always creates the builder's container `--privileged` `[BX-PRIVILEGED]`.
  - The default image ran buildkitd as root.
  - Each `RUN` step ran as real root (uid map `0 0 4294967295`), with Docker-like capabilities and a seccomp filter. Observed on 2026-10-04, BuildKit v0.33.1.
  - So a step that escapes BuildKit's sandbox through a kernel or runc bug is root on the host.
- **Docker daemon options evaluated** (P5.2):
  - **Rootless Docker** `[DK-ROOTLESS]`:
    - Container IPs live in RootlessKit's network namespace and are not reachable from the host. The worker's health probes dial container IPs (ARCHITECTURE §5 step 6).
    - AppArmor is unsupported, so app containers would lose Docker's default profile.
    - CPU, memory and pids limits (invariant 10) need cgroup v2 with systemd delegation.
    - Caddy's ports 80 and 443 need a sysctl or `setcap`, and a specific port driver to keep client source IPs.
    - Each of these is a redesign or a new failure mode on every host.
  - **userns-remap** `[DK-USERNS]`:
    - The containerd image store is not available with it `[DK-CONTAINERD]`. Shipyard depends on that store: the image ID is the manifest digest, and attestations load only there.
    - buildx would still run the builder with `--userns=host` `[BX-PRIVILEGED]`.
  - **gVisor** (`runsc`) for app containers `[GVISOR-DOCKER]`: a per-container runtime with a syscall compatibility and performance cost. Apps are trusted code in the MVP.
- **Rootless BuildKit** `[BK-ROOTLESS]`:
  - The `-rootless` image runs buildkitd as uid 1000 under RootlessKit. Build steps run in a user namespace: root maps to uid 1000, and uids 1–65536 map to 100000–165535.
  - BuildKit calls `--privileged` "almost safe for rootless".
  - Checked on WSL2 (Engine 29.8.1, buildx 0.37.1):
    - buildkitd runs as uid 1000;
    - a step's uid map is `0 1000 1`, `1 100000 65536`;
    - `adduser`, `chown` and `USER` build normally.
  - On Ubuntu 24.04 and later, unprivileged unconfined processes get no capabilities inside user namespaces they create `[UB-USERNS]`. BuildKit documents that rootless builds then need `kernel.apparmor_restrict_unprivileged_userns=0` `[BK-ROOTLESS]`.
  - The WSL kernel has no AppArmor, so this restriction could not be reproduced here.

## Decision

- **Build with rootless BuildKit.**
  - `build.DefaultImage` is `moby/buildkit:v0.33.1-rootless`, pinned to its multi-arch index digest. Until now the image floated on buildx's default.
  - The worker passes it as `--driver-opt image=…`.
- **Replace stale builders.**
  - `Builder.Ensure` compares the builder container's image with the configured one. On a mismatch it removes the builder (`buildx rm --force`, cache included) and creates it again, with ADR-0009's network and the limits.
  - So an upgrade from a rootful builder is automatic and costs one cold cache.
  - Updating the BuildKit version means changing the pinned image, and the next worker start replaces the builder.
- **Lift Ubuntu's user-namespace restriction, and offset it.**
  - On Ubuntu 24.04+, the host sets `kernel.apparmor_restrict_unprivileged_userns=0` (`deploy/sysctl/60-shipyard-buildkit.conf`). P5.7's installer applies it.
  - The three Shipyard units set `RestrictNamespaces=yes` `[SYSTEMD-EXEC]`. The internet-facing API, the worker and the backup job cannot create or join namespaces, sysctl or not.
  - App containers and the Caddy container keep Docker's default seccomp profile. Without `CAP_SYS_ADMIN`, which they drop, it denies `unshare`, `setns`, and `clone` with any namespace flag, including `CLONE_NEWUSER` `[DK-SECCOMP]`.
- **Keep the Docker daemon rootful**, without userns-remap or gVisor. Revisit them with untrusted repositories (P7).

## Consequences

- **Positive:**
  - A build step that escapes BuildKit's sandbox lands as host uid 1000 or 100000+, without capabilities, instead of root. buildkitd itself is not root either.
  - The builder image is pinned, so builds no longer change with buildx's floating default.
  - Upgrades replace an old builder by themselves, which also moves it onto ADR-0009's network.
  - Shipyard's own services cannot use namespaces at all.
- **Negative / risks:**
  - **The builder's container is still privileged:** no seccomp, no AppArmor, and host devices are visible. An escape is unprivileged but faces a large kernel surface.
  - **uid 1000 is often a real login user on cloud images** (for example `ubuntu`). An escaped step shares that uid on the host, though not that user's mount namespace or files, unless it also escapes the container's mounts.
  - **On Ubuntu 24.04+ the sysctl is host-wide.** Any unprivileged, unconfined process outside the Shipyard units (an operator's shell, other services) can again use namespaces' capabilities.
  - **`RestrictNamespaces=` is enforced only on some architectures.** It covers x86 and x86-64; arm64 is not in systemd's list `[SYSTEMD-EXEC]`.
  - **Rootless builds differ slightly.** Observed: a directory created and chowned in a step kept the setgid bit, `drwxr-sr-x` instead of `drwxr-xr-x`. Images built before and after this change may differ in such modes.
  - **One cold cache** on the first worker start after the upgrade.
- **Follow-ups:**
  - P5.7: the installer applies the sysctl on Ubuntu 24.04+ and checks it; the operator guide explains the trade-off.
  - P5.8: review the residual risk, together with ADR-0009.
  - P7: gVisor or a VM-based builder for untrusted repositories.

## Alternatives considered

- **Rootless only as an opt-in:** no host sysctl by default, but the default would stay "a build escape is host root", and an opt-in that is off protects nobody.
- **Defer, and only pin the image and add `RestrictNamespaces=`:** cheapest, but leaves the main risk as it is.
- **Run buildkitd ourselves, unprivileged, behind buildx's `remote` driver:** drops `--privileged` and could use an AppArmor profile that allows only buildkitd's user namespaces, instead of the host-wide sysctl. Several hundred lines plus an AppArmor profile to install and test on real Ubuntu hosts. Worth revisiting in P7.
- **Rootless Docker, userns-remap, gVisor:** see Context.
