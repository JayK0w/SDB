# syntax=docker/dockerfile:1

# --- Étape 1 : frontend ------------------------------------------------------
FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- Étape 2 : backend -------------------------------------------------------
# 1.25+ requis : les dépendances transitives otel du SDK Docker l'exigent
FROM golang:1.26-alpine AS backend
ARG VERSION=docker
WORKDIR /src
# couche modules séparée : cachée entre deux builds source-only
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/sdb ./cmd/sdb

# --- Étape 3 : runtime -------------------------------------------------------
# Distroless static : pas de shell ni gestionnaire de paquets, CA + tzdata
# inclus. Le processus tourne root car il parle au socket Docker (accès
# root-équivalent par nature) ; le vrai durcissement — rootfs read-only,
# no-new-privileges, cap_drop ALL, port loopback — vit dans compose.
FROM gcr.io/distroless/static-debian12 AS runtime

COPY --from=backend /out/sdb /usr/local/bin/sdb

ENV SDB_DATA_DIR=/data \
    SDB_LISTEN_HOST=0.0.0.0 \
    SDB_LISTEN_PORT=8080 \
    SDB_LOG_FORMAT=json

VOLUME /data
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sdb"]
