package domain

import "time"

// RestoreRecord is one row of restores_history: a single restore of one
// snapshot into one volume. It reuses BackupStatus for its lifecycle.
type RestoreRecord struct {
	ID            int64
	StorageID     int64
	SnapshotID    string
	TargetVolume  string
	ContainerID   string // container stopped during the restore, if any
	ContainerName string
	Status        BackupStatus
	StartTime     time.Time
	EndTime       *time.Time
	ErrorLog      string
}

// RestoreFilter narrows restores_history queries; zero values mean "any".
type RestoreFilter struct {
	TargetVolume string
	StorageID    int64
	Status       BackupStatus
	Limit        int
	Offset       int
}
