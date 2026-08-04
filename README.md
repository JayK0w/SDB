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

## How a restore runs

A restore has two modes, chosen in the restore dialog:

- **In place** — the snapshot overwrites the original volume. The source
  container is stopped for the duration (recommended) and restarted after,
  whatever the outcome.
- **Clone** — the snapshot is restored into a **new volume** that SDB
  creates, leaving the original untouched and the source container running.
  SDB then hands you a `docker-compose.clone.yml` so the clone can run
  **side by side** with the original — useful to inspect a backup, test a
  migration or diff two states without taking production down.

The clone works because the worker mounts the *target* volume at the path
under which the *source* volume was archived, and `--include` keeps that
same archived path — deriving it from the target name instead would point
at a path absent from the snapshot and restore nothing.

The generated compose file declares the image, the mount point and the
volume as `external: true`. Ports, environment, networks and command are
emitted **commented out**: the original's ports are already bound, and its
environment routinely holds secrets that must not be copied into a file.

## Production hardening

SDB holds the Docker socket (root-equivalent) **and** every repository
password. That makes it the single most valuable target in the deployment:
compromising it would otherwise mean losing production *and* its backups in
one move. The following controls exist to break that:

| Control | Default | Env var |
|---|---|---|
| **Append-only repositories** — SDB refuses `forget`/`prune` and deletion of the target. One-way switch: the API can enable it, never disable it. | off, per storage | — (UI/API) |
| **Restores are admin-only** — overwriting a production volume is a privileged action; the `user` role gets read-only history. | enforced | — |
| **Actor attribution** — every backup and restore records who triggered it (`system:schedule:<name>` for automated runs). Survives user deletion. | enforced | — |
| **Strict partial backups** — restic exit 3 (unreadable source files) fails the run instead of passing as a warning. | `true` | `SDB_BACKUP_STRICT_PARTIAL` |
| **Data-reading integrity checks** — `restic check --read-data-subset`; without it only repository structure is verified and silent pack corruption survives until a restore. | `5%` | `SDB_CHECK_READ_DATA_SUBSET` |
| **Outbound alerting** — non-blocking webhook on failed/warning runs. Payload carries no credentials, endpoints or raw restic output. `SDB_ALERT_FORMAT=slack` renders the Incoming Webhook schema (also understood by Mattermost and Rocket.Chat); the default `sdb` posts native JSON for a generic receiver. An unknown value refuses to start rather than silently falling back — a typo would otherwise produce alerts Slack rejects, unnoticed. | off | `SDB_ALERT_WEBHOOK`, `SDB_ALERT_FORMAT`, `SDB_ALERT_TIMEOUT` |
| **Revocable sessions** — every JWT carries the account's token generation, re-checked against the database on each request. Deleting an account, changing a role, changing a password, or `POST /users/:id/revoke-sessions` invalidates existing tokens immediately. Costs one integer read per authenticated request; caching it would reintroduce the revocation delay this removes. | enforced | — |
| **HTTP hardening** — strict CSP, `nosniff`, `DENY` framing, 1 MiB body cap, per-user rate limit on writes. HSTS only under TLS. | enforced | — |
| **Verification restores** — the latest snapshot of every repository is *actually extracted* into a disposable `sdb-verify-*` volume with `restic restore --verify`, then the volume is destroyed. | off | `SDB_VERIFY_INTERVAL` |
| **Missed-window detection** — schedules whose slot elapsed while SDB was down are logged and counted in `sdb_schedule_missed_runs_total`. Catch-up replays at most one run per schedule. | detection on, catch-up off | `SDB_SCHEDULE_CATCHUP` |
| **Secondary copies (3-2-1)** — a storage declared as the copy of another receives every snapshot through `restic copy`, right after each successful backup and again on a reconciliation pass. Replication lag is *measured in both repositories*, never remembered. | off until a copy exists, reconciliation every 6h | `SDB_REPLICATION_INTERVAL` |
| **Master key rotation** — `sdb rotate-master-key` re-encrypts every stored secret offline: consistent pre-rotation snapshot, single transaction, each value read back with the new key before commit. Deliberately not an API route. | on demand | `SDB_NEW_MASTER_KEY` |

**The append-only flag is an application-level ratchet, not immutability.**
It removes SDB as a deletion vector; it cannot stop someone who reaches the
repository directly. Pair it with server-side enforcement:

- Restic REST server: `rest-server --append-only`
- S3: Object Lock (compliance mode) or versioning + MFA delete
- Ideally, a *pull* topology where production cannot reach the backup store

### Proving the backups are restorable

`restic check` validates repository structure; `--read-data-subset` re-reads a
fraction of the blobs. Neither proves the files actually materialise. Only a
real restore does, so `SDB_VERIFY_INTERVAL` extracts the newest snapshot of
each repository into a throwaway volume with `--verify` (restic recomputes
every written file's hash against the snapshot) and then destroys the volume.

**The deadline is persisted, not restarted.** Each periodic pass (verification,
integrity check, replication reconciliation) records when it last ran, and on
startup schedules itself from *that* date. Waiting a full interval after every
boot — the previous behaviour — meant an instance restarted more often than the
interval never ran the pass at all: one weekly update with
`SDB_VERIFY_INTERVAL=168h` is enough, and nothing in the logs distinguished a
guarantee that never armed from one that was fine. An overdue pass now runs a
few minutes after startup and says so.

The run is recorded in the restore history as `system:verification`, so a
failure travels through the same alert path as any other failed job. Volume
deletion is guarded twice — the usecase only ever targets `sdb-verify-*`
names, and the Docker adapter refuses anything else, so a regression cannot
turn cleanup into data loss.

### The second copy (3-2-1)

Append-only protects a repository from *deletion*, not from the loss or
corruption of the medium holding it. One repository is one medium: losing it
loses everything. A storage created with `copy_of_storage_id` is the
**secondary copy** of another and receives its snapshots through
`restic copy`.

The link is carried by the copy, not by the source. That is what allows the
copy to be initialised with `restic init --copy-chunker-params` *from* its
source — chunker parameters can only be inherited at creation, and without
them copied data can occupy twice the space. It also makes several copies of
one repository a non-decision.

**It is optional, strongly advised, and can be switched on at any time.** SDB
runs without a secondary copy — it just says so on every startup, and the
storage page says it louder. Enabling it later requires no reconfiguration of
what already exists: creating the copy backfills the snapshots already in the
source repository straight away, in the background, so turning on 3-2-1
protects the existing history and not merely the backups that follow.

Two triggers, one mechanism:

- **after every successful backup**, before retention — pruning the primary
  first could delete a snapshot that still exists in only one place;
- **a reconciliation pass** (`SDB_REPLICATION_INTERVAL`) that copies whatever
  is missing, which is what catches up an unreachable destination, a network
  failure, or backups taken while SDB was down.

A failed copy degrades the run to **warning**, not failure: the snapshot
exists and is restorable from the primary, and announcing `failed` would
suggest there is no backup at all — the more dangerous lie. The gap stays
visible (the alert webhook fires on warnings) and stays counted in
`sdb_replication_pending_snapshots` / `sdb_replication_lag_seconds` until it
is closed.

**State is read from the repositories, never stored.** `restic copy`
re-encrypts, so a copied snapshot gets a *different* ID; replication is
tracked by matching snapshot time and archived paths across both
repositories. A "copied" column in SQLite could survive a database restore or
a manual purge of the copy and quietly lie; the comparison cannot.

Two constraints follow from restic itself, and both are enforced rather than
discovered at 3am:

- backend credentials (`AWS_*`, `B2_*`, …) have no `--from` variant and are
  **shared** between a repository and its copy source. Pairing two S3 accounts
  with different keys is refused at configuration time, naming the conflicting
  variable, instead of authenticating the copy against the wrong account;
- a copy target refuses direct backups. Mixing native and replicated snapshots
  in one repository would make the replication gap — and the alert built on it
  — meaningless.

`GET /replication` measures the gap on demand (two `restic snapshots` per
pair); `POST /storage/:id/replicate` forces a full pass.

### Runbook, RPO and RTO

Operating procedures live in **[docs/RUNBOOK.md](docs/RUNBOOK.md)** (French):
what to do when a verification fails, when a repository is corrupted, when the
secondary copy falls behind, how to restore without SDB at all, and how to
rotate the keys.

The two numbers an auditor asks for are **measured, not declared**:

- **RPO** — `sdb_last_backup_success_timestamp_seconds` gives the real age of
  the last *successful* backup per container. The announced RPO is the
  schedule interval plus the detection delay, and the detection delay is only
  short if `SDB_ALERT_WEBHOOK` is set.
- **RTO** — the verification restore is a real restore, so it is timed:
  `sdb_verification_restore_duration_seconds` is the measured basis for any
  announced recovery time. Without `SDB_VERIFY_INTERVAL` there is no
  measurement, and an RTO without a measurement is a promise.

Both gauges are **re-seeded from the database on startup**. Prometheus gauges
live in process memory: without seeding, every restart makes
"last successful backup" and "last proof of restorability" vanish, and an alert
built on `absent(...)` fires while nothing has happened. Counters are
deliberately *not* seeded — a counter restarting at zero is something
`rate()`/`increase()` handle, whereas one seeded to an arbitrary value makes
them lie.

Ready-to-load Prometheus rules are in
[deploy/prometheus/sdb-alerts.yml](deploy/prometheus/sdb-alerts.yml). They
cover the four ways to lose coverage without ever seeing a red run: nothing
backs up any more, a scheduled window was missed, the second copy fell behind,
and nothing proves restorability any more.

### Who backs up SDB itself

`sdb.db` (in the `sdb-data` volume) holds every repository password, encrypted
under the master key. Those passwords are **generated by SDB and never
returned by any read route** — `Redacted()` strips them and no endpoint
exposes them. Losing that volume therefore makes every repository
*permanently unreadable*: the master key would decrypt a file that no longer
exists.

Two things close that:

1. **Escrow the repository password.** `POST /storage` returns
   `restic_password` **once, at creation only** (the UI shows it with a
   "store this now" panel). Put it in your secret manager — with it you can
   open the repository with the `restic` CLI directly, no SDB involved. You
   may also *supply* your own password at creation (≥ 20 characters) instead
   of letting SDB generate one.
   There is deliberately **no export endpoint**: a permanent read path would
   hand a compromised admin every repository at once, which is the blast
   radius this design exists to contain.
2. **Back up `sdb-data`** like any other volume, to a repository whose
   password you hold independently.

### Integration tests

Most of the suite drives restic through test doubles: it proves SDB builds
the right commands, never that restic understands them. A restic upgrade
could change its `--json` output, an exit code or a flag name and every test
would stay green — the breakage would surface in production, at restore time.

`make test-integration` (a dedicated CI job, `-tags=integration`) runs the
real image against a real repository: init, backup, snapshot listing, clone
restore, `--verify`, `--read-data-subset` check, retention with prune, the
unknown-snapshot failure path, and the secondary copy — a snapshot copied to
a repository with a *different* password is restored **from that copy alone**
and compared byte for byte. It asserts on the *restored bytes*, not on the
command line — a mutation reintroducing the original clone bug fails it with
`restored content = ""`.

### Missed schedules

A schedule whose window elapsed while SDB was down is reported on startup
(log + `sdb_schedule_missed_runs_total`), which is what makes the hole
visible. Replaying it is opt-in (`SDB_SCHEDULE_CATCHUP`) and deliberately so:
a backup can **stop its container**, and firing ten of them while a host is
still recovering would take production down at the worst possible moment.
When enabled, each schedule replays **once** — never the whole backlog, whose
snapshots would deduplicate to the same data anyway.

## Layout (Clean Architecture)

```
cmd/sdb/            entrypoint (config, logging, graceful shutdown)
internal/domain/    entities + ports — zero framework imports
internal/usecase/   business orchestration (phase 3)
internal/infra/     SQLite, Docker SDK, Restic, crypto adapters (phase 2)
internal/api/http/  Gin REST API + WebSocket hub (phase 4)
web/                Vue 3 frontend (phase 5)
docs/               operating runbook + current deployment state
deploy/             Prometheus alert rules
```

New here? [docs/ETAT-DES-LIEUX.md](docs/ETAT-DES-LIEUX.md) (French) is the
resume point: what shipped, what this deployment actually runs, what is still
open.

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
| POST | `/storage/:id/verify` | admin | Verification restore — really extracts the newest snapshot into a throwaway volume → 202 |
| GET | `/replication` | user | Replication gap of every secondary copy (measured in both repositories) |
| POST | `/storage/:id/replicate` | admin | Copy everything missing to that secondary copy → 202 |
| POST | `/backups` | user | Start a backup → 202 + record |
| DELETE | `/backups/:id` | user | Cancel a running backup |
| GET | `/backups/history[/:id]` | user | Backup history (filterable) |
| POST | `/restores` | **admin** | Start a restore → 202 + record (`source_volume` ≠ `target_volume` clones) |
| DELETE | `/restores/:id` | **admin** | Cancel a running restore |
| GET | `/restores/history` | user | Restore history (filterable, shows the actor) |
| GET | `/restores/clone-compose` | **admin** | `docker-compose.yml` to run a clone alongside the original |
| GET/POST/PUT/DELETE | `/schedules[/:id]` | user | Recurring backups (cron, UTC) |
| POST | `/schedules/:id/run` | user | Fire a schedule now → 202 |
| GET | `/ws/metrics` | user | WebSocket event stream (`?token=`) |
| GET | `/metrics` | token | Prometheus metrics (static `SDB_METRICS_TOKEN`; disabled if unset) |
| GET/POST/PUT/DELETE | `/users...` | admin | User management (password change: self or admin) |
| POST | `/users/:id/revoke-sessions` | self or admin | Invalidate every token of that account |

## Storage backends

`local` (host path), `s3` (any S3-compatible: AWS, MinIO, Scaleway…),
`b2` (Backblaze), `azure` (Blob Storage), `gs` (Google Cloud Storage,
service account JSON in the `GOOGLE_CREDENTIALS_JSON` credential),
`sftp` (another server over SSH, private key in `SSH_PRIVATE_KEY`) and
`rest` (restic REST server). Credentials are stored AES-256-GCM
encrypted; key material is injected into the ephemeral worker as files
with 0600 permissions, never through the host filesystem.

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
- [x] v0.2 — scheduled backups (cron), restore history, TypeScript
      frontend, Prometheus `/metrics`, cloud backends (B2/Azure/GCS/SFTP)
- [x] Secondary copies (3-2-1) — `restic copy` to a second repository,
      inline after each backup plus a reconciliation pass, replication lag
      measured in both repositories
- [x] Operations — French runbook, measured RPO/RTO, Prometheus alert rules,
      offline master-key rotation (`sdb rotate-master-key`)
