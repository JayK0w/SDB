package domain

import "time"

// RestoreRecord : une ligne de restores_history.
type RestoreRecord struct {
	ID            int64
	StorageID     int64
	SnapshotID    string
	TargetVolume  string
	ContainerID   string // conteneur arrêté pendant la restauration, si demandé
	ContainerName string
	Status        BackupStatus
	StartTime     time.Time
	EndTime       *time.Time
	ErrorLog      string
}

// RestoreFilter : filtre de restores_history, zéro = ignoré.
type RestoreFilter struct {
	TargetVolume string
	StorageID    int64
	Status       BackupStatus
	Limit        int
	Offset       int
}
