package domain

import (
	"fmt"
	"time"
)

// BackupStatus : cycle de vie commun aux sauvegardes et restaurations.
type BackupStatus string

const (
	BackupPending  BackupStatus = "pending"
	BackupRunning  BackupStatus = "running"
	BackupSuccess  BackupStatus = "success"
	BackupWarning  BackupStatus = "warning" // terminé avec incidents non fatals
	BackupFailed   BackupStatus = "failed"
	BackupCanceled BackupStatus = "canceled"
)

func (s BackupStatus) Terminal() bool {
	switch s {
	case BackupSuccess, BackupWarning, BackupFailed, BackupCanceled:
		return true
	}
	return false
}

// Actor : qui a déclenché l'opération. Sauvegardes et restaurations sont
// destructrices ou coûteuses : l'historique doit pouvoir répondre à « qui a
// lancé ça ? » sans recouper les logs. UserID 0 = déclenchement interne
// (planificateur), jamais un humain.
type Actor struct {
	UserID int64
	Name   string
}

// SystemActor : déclencheur non humain, nommé d'après sa source.
func SystemActor(source string) Actor { return Actor{UserID: 0, Name: "system:" + source} }

// String : libellé stable pour les logs et l'historique.
func (a Actor) String() string {
	if a.Name == "" {
		return "unknown"
	}
	return a.Name
}

// BackupRecord : une ligne de backups_history.
type BackupRecord struct {
	ID             int64
	ContainerID    string
	ContainerName  string
	StorageID      int64
	Status         BackupStatus
	BytesProcessed int64
	SnapshotID     string
	TriggeredBy    Actor
	StartTime      time.Time
	EndTime        *time.Time
	ErrorLog       string
}

// BackupRequest : tout ce qu'un run de sauvegarde requiert.
type BackupRequest struct {
	ContainerID   string
	StorageID     int64
	Volumes       []string // sous-ensemble ; vide = tous les montages sauvegardables
	StopContainer bool     // sauvegarde à froid, redémarrage garanti ensuite
	PreHook       *Hook
	PostHook      *Hook
	Retention     *RetentionPolicy // forget --prune après succès
	Tags          []string
	// TriggeredBy : renseigné par la couche de livraison depuis le JWT, ou
	// par le planificateur pour un run automatique.
	TriggeredBy Actor
}

func (r *BackupRequest) Validate() error {
	if r.ContainerID == "" {
		return fmt.Errorf("%w: container id is required", ErrInvalidInput)
	}
	if r.StorageID <= 0 {
		return fmt.Errorf("%w: storage id is required", ErrInvalidInput)
	}
	if r.PreHook != nil {
		if err := r.PreHook.Validate(); err != nil {
			return fmt.Errorf("pre-hook: %w", err)
		}
	}
	if r.PostHook != nil {
		if err := r.PostHook.Validate(); err != nil {
			return fmt.Errorf("post-hook: %w", err)
		}
	}
	if r.Retention != nil {
		if err := r.Retention.Validate(); err != nil {
			return fmt.Errorf("retention: %w", err)
		}
	}
	return nil
}

// Snapshot : métadonnées d'un snapshot restic.
type Snapshot struct {
	ID       string
	ShortID  string
	Time     time.Time
	Hostname string
	Paths    []string
	Tags     []string
}

// BackupSummary : résultat final parsé du message summary de restic.
type BackupSummary struct {
	SnapshotID      string
	FilesNew        int64
	FilesChanged    int64
	FilesUnmodified int64
	BytesAdded      int64
	BytesProcessed  int64
	Duration        time.Duration
}

// HistoryFilter : filtre de backups_history, zéro = ignoré.
type HistoryFilter struct {
	ContainerID string
	StorageID   int64
	Status      BackupStatus
	Limit       int
	Offset      int
}
