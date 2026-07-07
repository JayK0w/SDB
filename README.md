# Standalone Docker Backup (SDB)

Self-hosted, security-first backup orchestrator for Docker volumes, built on
[Restic](https://restic.net) (AES-256 encrypted, deduplicated snapshots) with
a Go backend and a Vue 3 dashboard.

> Status: feature-complete v0.1 — all six build phases done.

## Quick start (Docker)

```sh
mkdir -p secrets
openssl rand -hex 32 > secrets/sdb_master_key
openssl rand -hex 32 > secrets/sdb_jwt_secret
docker compose up -d --build
docker compose logs sdb | grep "initial admin"   # first-login password
```

Then open http://127.0.0.1:8080 — the Vue dashboard is embedded in the
binary. The compose file applies the full hardening described below.

## How a backup runs

1. SDB lists containers through the Docker SDK.
2. Before the snapshot it executes the configured **pre-hook** inside the
   target container (e.g. `pg_dumpall`) and/or **stops** the container for
   cold consistency.
3. The target volumes are mounted **read-only** into an ephemeral worker
   container that runs Restic against the configured repository (local
   path, S3, SFTP or Restic REST server).
4. The container is restarted, the post-hook runs, and the retention policy
   (`restic forget --prune`) is applied.
5. Progress — parsed from Restic's JSON output — is streamed live to the UI
   through a non-blocking WebSocket hub. Any Docker error triggers a
   rollback that restarts the source container and surfaces the error to
   the frontend.

## Layout (Clean Architecture)

```
cmd/sdb/            entrypoint (config, logging, graceful shutdown)
internal/domain/    entities + ports — zero framework imports
internal/usecase/   business orchestration (phase 3)
internal/infra/     SQLite, Docker SDK, Restic, crypto adapters (phase 2)
internal/api/http/  Gin REST API + WebSocket hub (phase 4)
web/                Vue 3 frontend (phase 5)
```

The domain layer defines every port (`UserRepository`, `ContainerRuntime`,
`SnapshotEngine`, `Cipher`, ...); outer layers depend inward only.

## API

All routes live under `/api/v1` and require a JWT (`Authorization: Bearer`)
except login. Long operations return **202 Accepted** and stream progress
over the WebSocket.

| Method | Route | Role | Description |
|---|---|---|---|
| POST | `/auth/login` | — | Returns a JWT (rate-limited) |
| GET | `/health` | user | Global state (North Star indicator) |
| GET | `/containers` | user | List containers (`?all=true` includes stopped) |
| GET | `/storage` | user | List storage targets (secrets redacted) |
| POST/PUT/DELETE | `/storage[/:id]` | admin | Manage storage targets |
| GET | `/storage/:id/snapshots` | user | List Restic snapshots (`?tag=`) |
| POST | `/storage/:id/check` | admin | Integrity check → 202 |
| POST | `/backups` | user | Start a backup → 202 + record |
| DELETE | `/backups/:id` | user | Cancel a running backup |
| GET | `/backups/history[/:id]` | user | Backup history (filterable) |
| POST | `/restores` | user | Start a restore → 202 |
| GET | `/ws/metrics` | user | WebSocket event stream (`?token=`) |
| GET/POST/PUT/DELETE | `/users...` | admin | User management (password change: self or admin) |

## Configuration

Environment-first — see [.env.example](.env.example) for the full reference.
`SDB_MASTER_KEY` and `SDB_JWT_SECRET` are required (32+ characters,
`openssl rand -hex 32`); both support the `_FILE` suffix for Docker secrets.

## Security model

- The API binds to `127.0.0.1` by default. When publishing the container
  port, keep the host-side mapping loopback-only (`127.0.0.1:8080:8080`):
  Docker port publishing bypasses UFW/iptables.
- `tcp://` Docker daemon endpoints are **refused at startup** unless full
  mTLS material (CA + client cert + key) is configured.
- User passwords are hashed with Argon2id. Storage credentials and Restic
  repository passwords are encrypted at rest with AES-256-GCM under the
  master key and decrypted in memory only when a Restic process is spawned.
- Repositories themselves are end-to-end encrypted by Restic (AES-256).
- The SDB container ships hardened (see [docker-compose.yml](docker-compose.yml)):
  `read_only` root filesystem, `no-new-privileges`, **all** capabilities
  dropped, writable state confined to the `/data` volume and `tmpfs /tmp`,
  secrets mounted as files rather than environment variables.

## Development

Backend (Go 1.23+):

```
go mod tidy # first build only: resolves modules and writes go.sum
make run    # start the daemon
make test   # unit tests (race detector enabled)
make build  # static binary in bin/sdb
```

Frontend (Node 20+), in a second terminal:

```
cd web
npm install
npm run dev   # http://localhost:5173, /api proxied to the Go backend
```

The Vite dev server proxies `/api` (HTTP and WebSocket) to
`127.0.0.1:8080`; log in with the admin account printed in the backend
logs on first start.

## Roadmap

- [x] Phase 1 — foundations: domain model, ports, configuration
- [x] Phase 2 — infrastructure: SQLite, crypto, Restic wrapper, Docker runtime
- [x] Phase 3 — usecases: backup/restore orchestration, retention, integrity checks, auth
- [x] Phase 4 — REST API (Gin), JWT auth, WebSocket hub
- [x] Phase 5 — Vue 3 dashboard (dark-mode-first, North Star indicator, live metrics)
- [x] Phase 6 — hardened packaging: multi-stage image, compose file, CI

Next candidates: scheduled backups (cron), restore history, TypeScript
migration of the frontend, Prometheus metrics endpoint.
