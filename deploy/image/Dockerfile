# Release image built by GoReleaser (dockers_v2) from prebuilt static binaries.
# It is not a source build: the binaries arrive in $TARGETPLATFORM/.
#
# The supported production install is systemd (deploy/README.md). This image
# is for running the CLI and for evaluation. Running the worker in a container
# would require mounting the Docker socket, which is root-equivalent [DK-POSTINSTALL].
#
# Base: distroless static (CA certificates, tzdata, nonroot user), pinned by digest.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/shipyard $TARGETPLATFORM/shipyard-api $TARGETPLATFORM/shipyard-worker /usr/local/bin/
USER nonroot:nonroot
CMD ["shipyard", "help"]
