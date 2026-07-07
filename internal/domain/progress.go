package domain

import "time"

// EventType classifies messages streamed to the frontend over the
// WebSocket hub while a backup or restore is running.
type EventType string

const (
	EventLog      EventType = "log"      // raw Restic stdout/stderr line
	EventProgress EventType = "progress" // percentage and byte/file counters
	EventStatus   EventType = "status"   // lifecycle transition (pending -> running -> ...)
	EventSummary  EventType = "summary"  // terminal event carrying the final numbers
	EventError    EventType = "error"    // non-fatal error line (rendered red by the UI)
)

// ProgressEvent is the unit published through the hub. The JSON tags are
// the WebSocket wire format consumed by the Vue frontend, which makes this
// struct the single source of truth for that contract.
type ProgressEvent struct {
	BackupID   int64        `json:"backup_id"`
	Type       EventType    `json:"type"`
	Time       time.Time    `json:"time"`
	Status     BackupStatus `json:"status,omitempty"`
	Message    string       `json:"message,omitempty"`
	Percent    float64      `json:"percent,omitempty"`
	BytesDone  int64        `json:"bytes_done,omitempty"`
	TotalBytes int64        `json:"total_bytes,omitempty"`
	FilesDone  int64        `json:"files_done,omitempty"`
	TotalFiles int64        `json:"total_files,omitempty"`
	SnapshotID string       `json:"snapshot_id,omitempty"`
}
