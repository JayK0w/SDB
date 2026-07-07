# syntax=docker/dockerfile:1

# --- Stage 1: frontend -------------------------------------------------------
FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json ./
RUN npm install
COPY web/ ./
RUN npm run build

# --- Stage 2: backend --------------------------------------------------------
# 1.25+: `go mod tidy` resolves docker/docker's transitive otel deps at
# their latest versions, which require a recent toolchain.
FROM golang:1.25-alpine AS backend
ARG VERSION=docker
WORKDIR /src
# Modules are pinned by go.sum; keeping this layer separate caches them
# across source-only rebuilds.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/sdb ./cmd/sdb

# --- Stage 3: runtime --------------------------------------------------------
# Distroless static: no shell, no package manager, CA certificates and
# tzdata included. The process runs as root because it must talk to the
# host's Docker socket (root-equivalent by design); the actual hardening —
# read-only rootfs, no-new-privileges, cap_drop ALL, loopback-only port —
# is applied at runtime by docker-compose.yml.
FROM gcr.io/distroless/static-debian12 AS runtime

COPY --from=backend /out/sdb /usr/local/bin/sdb

ENV SDB_DATA_DIR=/data \
    SDB_LISTEN_HOST=0.0.0.0 \
    SDB_LISTEN_PORT=8080 \
    SDB_LOG_FORMAT=json

VOLUME /data
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sdb"]
