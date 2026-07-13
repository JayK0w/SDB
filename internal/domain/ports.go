package domain

import (
	"context"
	"io"
	"time"
)

// --- Ports de persistance (implémentés par internal/infra/sqlite) ---

type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id int64) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id int64) error
	Count(ctx context.Context) (int64, error) // détection premier démarrage
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
	// FailInterrupted : au démarrage, bascule en failed les runs restés
	// pending/running après un crash.
	FailInterrupted(ctx context.Context, reason string) (int64, error)
}

type RestoreHistoryRepository interface {
	Create(ctx context.Context, rec *RestoreRecord) error
	Update(ctx context.Context, rec *RestoreRecord) error
	GetByID(ctx context.Context, id int64) (*RestoreRecord, error)
	List(ctx context.Context, filter RestoreFilter) ([]RestoreRecord, error)
	FailInterrupted(ctx context.Context, reason string) (int64, error)
}

type ScheduleRepository interface {
	Create(ctx context.Context, s *BackupSchedule) error
	GetByID(ctx context.Context, id int64) (*BackupSchedule, error)
	List(ctx context.Context) ([]BackupSchedule, error)
	Update(ctx context.Context, s *BackupSchedule) error
	Delete(ctx context.Context, id int64) error
	TouchLastRun(ctx context.Context, id int64, at time.Time) error
}

// --- Port runtime conteneurs (implémenté par internal/infra/docker) ---

// WorkerSpec : conteneur éphémère qui exécute restic.
type WorkerSpec struct {
	Image string
	Cmd   []string
	// Env contient les secrets RESTIC_* : ne jamais logger.
	Env []string
	// Files : secrets écrits en 0600 dans le worker avant démarrage
	// (clé SSH, compte de service GCS). Ne jamais logger.
	Files       map[string][]byte
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
	// Exec : lance un hook dans un conteneur en marche, tué au timeout.
	Exec(ctx context.Context, id string, cmd []string, timeout time.Duration) (*ExecResult, error)
	// RunWorker : démarre le worker, stream sa sortie, bloque jusqu'à sa
	// fin. Garanties : worker toujours supprimé (même sur annulation),
	// aucune écriture sur stdout/stderr après le retour.
	RunWorker(ctx context.Context, spec WorkerSpec, stdout, stderr io.Writer) (exitCode int, err error)
}

// --- Port moteur de snapshots (implémenté par internal/infra/restic) ---

type SnapshotEngine interface {
	// EnsureRepository : initialise le dépôt s'il n'existe pas encore.
	EnsureRepository(ctx context.Context, storage *StorageConfig) error
	// Backup : snapshot des montages, événements poussés en continu.
	Backup(ctx context.Context, storage *StorageConfig, backupID int64, mounts []Mount, tags []string, events chan<- ProgressEvent) (*BackupSummary, error)
	// Restore : extrait un snapshot dans le volume cible (worker en écriture).
	Restore(ctx context.Context, storage *StorageConfig, snapshotID, targetVolume string, events chan<- ProgressEvent) error
	Snapshots(ctx context.Context, storage *StorageConfig, tags []string) ([]Snapshot, error)
	// Forget : applique la rétention (restic forget, --prune si demandé).
	Forget(ctx context.Context, storage *StorageConfig, policy RetentionPolicy) error
	// Check : vérification d'intégrité du dépôt.
	Check(ctx context.Context, storage *StorageConfig) error
}

// --- Ports sécurité (implémentés par internal/infra/crypto) ---

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

// Cipher : chiffrement des secrets au repos sous la clé maître.
type Cipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// --- Port de diffusion (hub WebSocket, collecteur Prometheus) ---

// EventPublisher : Publish ne doit JAMAIS bloquer — un consommateur lent
// est abandonné, pas attendu.
type EventPublisher interface {
	Publish(event ProgressEvent)
}
