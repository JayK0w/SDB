package domain

import "time"

type EventType string

const (
	EventLog      EventType = "log"      // ligne brute de restic
	EventProgress EventType = "progress" // compteurs pourcentage/octets
	EventStatus   EventType = "status"   // transition de cycle de vie
	EventSummary  EventType = "summary"  // bilan final
	EventError    EventType = "error"    // erreur non fatale (rouge côté UI)
)

// ProgressEvent : unité diffusée par le hub. Les tags JSON SONT le contrat
// WebSocket consommé par le frontend. BackupID ou RestoreID, jamais les deux.
type ProgressEvent struct {
	BackupID   int64        `json:"backup_id,omitempty"`
	RestoreID  int64        `json:"restore_id,omitempty"`
	Container  string       `json:"container,omitempty"`
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
