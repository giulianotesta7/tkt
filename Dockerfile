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
COPY --from=build --chown=65532:65532 /out/server /server
COPY --from=build --chown=65532:65532 /data /data
EXPOSE 8080
ENTRYPOINT ["/server"]
