CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'user')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

-- credentials_enc and restic_password_enc hold AES-256-GCM blobs sealed
-- under the SDB master key; plaintext secrets never touch the disk.
CREATE TABLE storage_configs (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT NOT NULL UNIQUE,
    type                TEXT NOT NULL CHECK (type IN ('local', 's3', 'sftp', 'rest')),
    endpoint            TEXT NOT NULL,
    credentials_enc     BLOB NOT NULL,
    restic_password_enc BLOB NOT NULL,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

CREATE TABLE backups_history (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id    TEXT NOT NULL,
    container_name  TEXT NOT NULL DEFAULT '',
    storage_id      INTEGER NOT NULL REFERENCES storage_configs (id) ON DELETE RESTRICT,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'running', 'success', 'warning', 'failed', 'canceled')),
    bytes_processed INTEGER NOT NULL DEFAULT 0,
    snapshot_id     TEXT NOT NULL DEFAULT '',
    start_time      TEXT NOT NULL,
    end_time        TEXT,
    error_log       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_history_container ON backups_history (container_id);
CREATE INDEX idx_history_storage ON backups_history (storage_id);
CREATE INDEX idx_history_status ON backups_history (status);
CREATE INDEX idx_history_start ON backups_history (start_time DESC);
