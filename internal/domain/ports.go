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
	// TokenVersion : génération courante des jetons du compte. Lecture
	// dédiée plutôt qu'un GetByID complet : elle est faite à CHAQUE requête
	// authentifiée, et il n'y a aucune raison de promener le hash du mot de
	// passe dans la couche de livraison à cette fréquence.
	// ErrNotFound si le compte n'existe plus — ses jetons doivent alors
	// cesser d'être acceptés.
	TokenVersion(ctx context.Context, id int64) (int64, error)
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

// MaintenanceStateRepository : date du dernier passage des boucles
// périodiques (vérification, contrôle d'intégrité, réconciliation).
//
// Sans persistance, une échéance repart de zéro à CHAQUE démarrage : sur une
// instance redémarrée plus souvent que l'intervalle, la passe ne s'exécute
// jamais — et une garantie qui ne s'arme jamais ne se distingue pas, dans les
// logs, d'une garantie qui va bien.
type MaintenanceStateRepository interface {
	// LastRun : zéro si la tâche n'a jamais tourné.
	LastRun(ctx context.Context, task string) (time.Time, error)
	MarkRun(ctx context.Context, task string, at time.Time) error
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
	// RemoveVolume : supprime un volume Docker. Destructif : l'implémen-
	// tation DOIT refuser tout nom hors du préfixe des volumes jetables de
	// SDB (cf. VerifyVolumePrefix), pour qu'un défaut d'appel ne puisse pas
	// détruire un volume de production.
	RemoveVolume(ctx context.Context, name string) error
}

// VerifyVolumePrefix : préfixe réservé aux volumes jetables créés par les
// restaurations de vérification. Seuls ceux-là sont supprimables par SDB.
const VerifyVolumePrefix = "sdb-verify-"

// IsScratchVolume : le volume est un jetable de vérification.
func IsScratchVolume(name string) bool {
	return len(name) > len(VerifyVolumePrefix) && name[:len(VerifyVolumePrefix)] == VerifyVolumePrefix
}

// --- Port moteur de snapshots (implémenté par internal/infra/restic) ---

type SnapshotEngine interface {
	// EnsureRepository : initialise le dépôt s'il n'existe pas encore.
	EnsureRepository(ctx context.Context, storage *StorageConfig) error
	// Backup : snapshot des montages, événements poussés en continu.
	Backup(ctx context.Context, storage *StorageConfig, backupID int64, mounts []Mount, tags []string, events chan<- ProgressEvent) (*BackupSummary, error)
	// Restore : extrait un snapshot dans le volume cible (worker en
	// écriture), cf. RestoreSpec.
	Restore(ctx context.Context, storage *StorageConfig, spec RestoreSpec, events chan<- ProgressEvent) error
	Snapshots(ctx context.Context, storage *StorageConfig, tags []string) ([]Snapshot, error)
	// EnsureCopyTarget : initialise le dépôt de copie en HÉRITANT des
	// paramètres de découpage de sa source (`init --copy-chunker-params`) ;
	// no-op s'il existe déjà. Sans cet héritage, restic ne re-découpe pas les
	// fichiers copiés et les données peuvent occuper le double dans la copie.
	EnsureCopyTarget(ctx context.Context, dst, src *StorageConfig) error
	// Copy : recopie des snapshots de src vers dst (restic copy). Liste vide =
	// tous ; restic saute ceux déjà présents. Les deux dépôts sont ouverts par
	// le MÊME worker : leurs identifiants de backend partagent un unique jeu de
	// variables d'environnement, d'où le refus des paires en conflit.
	Copy(ctx context.Context, dst, src *StorageConfig, snapshotIDs []string, events chan<- ProgressEvent) error
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
