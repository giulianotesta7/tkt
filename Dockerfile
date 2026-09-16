# syntax=docker/dockerfile:1
# Multi-stage build: CGO_ENABLED=0 (modernc sqlite is pure Go) into a
# distroless static image (task 6.2).
FROM golang:1.25.14 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
# Prepare the data directory with non-root ownership: the distroless image
# runs as UID 65532, and a fresh named volume inherits this directory's
# ownership on first mount, so the server can create /data/tkt.db.
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG REVISION=unknown
# OCI labels identify the built artifact. The defaults are development
# values; a publish workflow would pass the real version and commit as build
# args. No .licenses label: the repository has no LICENSE file.
LABEL org.opencontainers.image.title="tkt" \
      org.opencontainers.image.description="Single-binary ticket tracker on an embedded SQLite database" \
      org.opencontainers.image.source="https://github.com/giulianotesta7/tkt" \
      org.opencontainers.image.url="https://github.com/giulianotesta7/tkt" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION
COPY --from=build --chown=65532:65532 /out/server /server
COPY --from=build --chown=65532:65532 /data /data
# The image, not compose, owns the runtime defaults and the healthcheck, so
# every way of running the image gets the same contract. USER and STOPSIGNAL
# restate the distroless nonroot defaults on purpose: the contract should be
# readable in this file, not discovered by inspecting the base image.
ENV TKT_DB_PATH=/data/tkt.db
ENV TKT_LISTEN=:8080
EXPOSE 8080
USER 65532:65532
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/server","-healthcheck"]
ENTRYPOINT ["/server"]
