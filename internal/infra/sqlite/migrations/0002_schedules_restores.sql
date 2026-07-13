-- v0.2 : planifications, historique des restaurations, nouveaux backends
-- (b2, azure, gs). La validation du type passe au domaine : le CHECK est
-- retiré via un rebuild de table (exécuté avec foreign_keys=OFF).

CREATE TABLE storage_configs_new (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT NOT NULL UNIQUE,
    type                TEXT NOT NULL,
    endpoint            TEXT NOT NULL,
    credentials_enc     BLOB NOT NULL,
    restic_password_enc BLOB NOT NULL,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
INSERT INTO storage_configs_new
    SELECT id, name, type, endpoint, credentials_enc, restic_password_enc, created_at, updated_at
    FROM storage_configs;
DROP TABLE storage_configs;
ALTER TABLE storage_configs_new RENAME TO storage_configs;

-- hooks/retention/volumes/tags : documents JSON opaques
CREATE TABLE backup_schedules (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL UNIQUE,
    cron           TEXT NOT NULL,
    enabled        INTEGER NOT NULL DEFAULT 1,
    container_name TEXT NOT NULL,
    storage_id     INTEGER NOT NULL REFERENCES storage_configs (id) ON DELETE RESTRICT,
    volumes        TEXT NOT NULL DEFAULT '[]',
    stop_container INTEGER NOT NULL DEFAULT 0,
    pre_hook       TEXT,
    post_hook      TEXT,
    retention      TEXT,
    tags           TEXT NOT NULL DEFAULT '[]',
    last_run_at    TEXT,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE TABLE restores_history (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id     INTEGER NOT NULL REFERENCES storage_configs (id) ON DELETE RESTRICT,
    snapshot_id    TEXT NOT NULL,
    target_volume  TEXT NOT NULL,
    container_id   TEXT NOT NULL DEFAULT '',
    container_name TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL CHECK (status IN ('pending', 'running', 'success', 'warning', 'failed', 'canceled')),
    start_time     TEXT NOT NULL,
    end_time       TEXT,
    error_log      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_restores_volume ON restores_history (target_volume);
CREATE INDEX idx_restores_start ON restores_history (start_time DESC);
