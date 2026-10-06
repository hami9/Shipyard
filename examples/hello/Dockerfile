# The acceptance demo's sample app (docs/ACCEPTANCE.md). The build has a RUN
# step, so it also exercises rootless BuildKit (ADR-0010).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /hello .

# No shell and no root: Shipyard runs it with every capability dropped.
FROM scratch
COPY --from=build /hello /hello
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/hello"]
