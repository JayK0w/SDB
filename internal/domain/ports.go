package domain

import (
	"context"
	"io"
	"time"
)

// ---------------------------------------------------------------------------
// Persistence ports — implemented by internal/infra/sqlite.
// ---------------------------------------------------------------------------

type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id int64) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id int64) error
	// Count lets the bootstrap detect first boot and create the initial
	// admin account.
	Count(ctx context.Context) (int64, error)
}

type StorageRepository interface {
	Create(ctx context.Context, cfg *StorageConfig) error
	GetByID(ctx context.Context, id int64) (*StorageConfig, error)
	List(ctx context.Context) ([]StorageConfig, error)
	Update(ctx context.Context, cfg *StorageConfig) error
	Delete(ctx context.Context, id int64) error
}

type BackupHistoryRepository interface {
	Create(ctx context.Context, rec *BackupRecord) error
	Update(ctx context.Context, rec *BackupRecord) error
	GetByID(ctx context.Context, id int64) (*BackupRecord, error)
	List(ctx context.Context, filter HistoryFilter) ([]BackupRecord, error)
	// FailInterrupted marks every non-terminal record as failed. Called at
	// startup so runs interrupted by a crash do not stay "running" forever.
	FailInterrupted(ctx context.Context, reason string) (int64, error)
}

// ---------------------------------------------------------------------------
// Container runtime port — implemented by internal/infra/docker.
// ---------------------------------------------------------------------------

// WorkerSpec describes the ephemeral container SDB spawns to run Restic
// against the target volumes.
type WorkerSpec struct {
	Image string
	Cmd   []string
	// Env carries RESTIC_* variables including secrets: implementations
	// and callers must never log this field.
	Env []string
	// Mounts are attached to the worker; the runtime forces ReadOnly=true
	// on every mount unless the spec explicitly allows writing (restores).
	Mounts      []Mount
	Labels      map[string]string
	NetworkMode string
}

type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type ContainerRuntime interface {
	Ping(ctx context.Context) error
	List(ctx context.Context, all bool) ([]Container, error)
	Get(ctx context.Context, id string) (*Container, error)
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Start(ctx context.Context, id string) error
	// Exec runs a hook command inside a running container and captures its
	// output. The command is killed when the timeout elapses.
	Exec(ctx context.Context, id string, cmd []string, timeout time.Duration) (*ExecResult, error)
	// RunWorker starts the ephemeral backup worker, streams its output to
	// stdout/stderr and blocks until it exits. The worker container is
	// always removed afterwards, even on error or context cancellation,
	// and no write to stdout/stderr happens after RunWorker returns.
	RunWorker(ctx context.Context, spec WorkerSpec, stdout, stderr io.Writer) (exitCode int, err error)
}

// ---------------------------------------------------------------------------
// Snapshot engine port — implemented by internal/infra/restic.
// ---------------------------------------------------------------------------

type SnapshotEngine interface {
	// EnsureRepository initialises the Restic repository if it does not
	// exist yet; it is a no-op on an already initialised repository.
	EnsureRepository(ctx context.Context, storage *StorageConfig) error
	// Backup snapshots the given mounts and pushes ProgressEvents parsed
	// from Restic's JSON output while running. It returns the summary of
	// the created snapshot.
	Backup(ctx context.Context, storage *StorageConfig, backupID int64, mounts []Mount, tags []string, events chan<- ProgressEvent) (*BackupSummary, error)
	// Restore extracts a snapshot into the target volume through a
	// read-write worker.
	Restore(ctx context.Context, storage *StorageConfig, snapshotID, targetVolume string, events chan<- ProgressEvent) error
	Snapshots(ctx context.Context, storage *StorageConfig, tags []string) ([]Snapshot, error)
	// Forget applies the retention policy (restic forget, plus --prune
	// when the policy asks for it).
	Forget(ctx context.Context, storage *StorageConfig, policy RetentionPolicy) error
	// Check verifies repository integrity (scheduled maintenance).
	Check(ctx context.Context, storage *StorageConfig) error
}

// ---------------------------------------------------------------------------
// Security ports — implemented by internal/infra/crypto.
// ---------------------------------------------------------------------------

// PasswordHasher hashes user passwords (Argon2id).
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

// Cipher protects secrets at rest (storage credentials, Restic repository
// passwords) with AES-256-GCM under the SDB master key.
type Cipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// ---------------------------------------------------------------------------
// Delivery ports — implemented by the WebSocket hub (internal/api/http).
// ---------------------------------------------------------------------------

// EventPublisher fans ProgressEvents out to connected clients. Publish
// must never block the backup goroutine: slow consumers are dropped, not
// waited for.
type EventPublisher interface {
	Publish(event ProgressEvent)
}
