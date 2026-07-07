package domain

import (
	"fmt"
	"time"
)

// BackupStatus is the lifecycle of a backup or restore run.
type BackupStatus string

const (
	BackupPending  BackupStatus = "pending"
	BackupRunning  BackupStatus = "running"
	BackupSuccess  BackupStatus = "success"
	BackupWarning  BackupStatus = "warning" // finished, but a non-fatal step failed (e.g. post-hook)
	BackupFailed   BackupStatus = "failed"
	BackupCanceled BackupStatus = "canceled"
)

// Terminal reports whether the run reached a final state.
func (s BackupStatus) Terminal() bool {
	switch s {
	case BackupSuccess, BackupWarning, BackupFailed, BackupCanceled:
		return true
	}
	return false
}

// BackupRecord is one row of backups_history: a single backup attempt of
// one container towards one storage backend.
type BackupRecord struct {
	ID             int64
	ContainerID    string
	ContainerName  string
	StorageID      int64
	Status         BackupStatus
	BytesProcessed int64
	SnapshotID     string
	StartTime      time.Time
	EndTime        *time.Time
	ErrorLog       string
}

func (r *BackupRecord) Duration() time.Duration {
	if r.EndTime == nil {
		return 0
	}
	return r.EndTime.Sub(r.StartTime)
}

// BackupRequest carries everything a backup run needs. It is built by the
// API layer from user input and consumed by the backup usecase.
type BackupRequest struct {
	ContainerID string
	StorageID   int64
	// Volumes restricts the run to these volume names; empty means every
	// backupable mount of the container.
	Volumes []string
	// StopContainer stops the container for the duration of the snapshot
	// (cold backup). The container is restarted afterwards no matter how
	// the run ends.
	StopContainer bool
	PreHook       *Hook
	PostHook      *Hook
	// Retention, when set, applies restic forget (and optionally --prune)
	// after a successful backup.
	Retention *RetentionPolicy
	Tags      []string
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

// Snapshot mirrors the metadata Restic reports for a stored snapshot.
type Snapshot struct {
	ID       string
	ShortID  string
	Time     time.Time
	Hostname string
	Paths    []string
	Tags     []string
}

// BackupSummary is the final result reported by the snapshot engine,
// parsed from Restic's JSON summary message.
type BackupSummary struct {
	SnapshotID      string
	FilesNew        int64
	FilesChanged    int64
	FilesUnmodified int64
	BytesAdded      int64
	BytesProcessed  int64
	Duration        time.Duration
}

// HistoryFilter narrows backups_history queries; zero values mean "any".
type HistoryFilter struct {
	ContainerID string
	StorageID   int64
	Status      BackupStatus
	Limit       int
	Offset      int
}
