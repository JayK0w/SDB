package restic

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// send : tolère un canal nil, abandonne si le contexte est annulé — un
// consommateur bloqué ne doit pas geler le pipeline.
func send(ctx context.Context, events chan<- domain.ProgressEvent, ev domain.ProgressEvent) {
	if events == nil {
		return
	}
	select {
	case events <- ev:
	case <-ctx.Done():
	}
}

// resticMessage : union des lignes JSON émises par restic --json.
type resticMessage struct {
	MessageType string `json:"message_type"`

	// status (backup et restore)
	PercentDone float64 `json:"percent_done"` // 0..1
	TotalFiles  int64   `json:"total_files"`
	FilesDone   int64   `json:"files_done"`
	TotalBytes  int64   `json:"total_bytes"`
	BytesDone   int64   `json:"bytes_done"`
	// compteurs spécifiques restore
	FilesRestored int64 `json:"files_restored"`
	BytesRestored int64 `json:"bytes_restored"`

	// summary (backup)
	FilesNew            int64   `json:"files_new"`
	FilesChanged        int64   `json:"files_changed"`
	FilesUnmodified     int64   `json:"files_unmodified"`
	DataAdded           int64   `json:"data_added"`
	TotalBytesProcessed int64   `json:"total_bytes_processed"`
	TotalDuration       float64 `json:"total_duration"`
	SnapshotID          string  `json:"snapshot_id"`

	// error
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	During string `json:"during"`
	Item   string `json:"item"`
}

// decodeBackupLine : une ligne restic → ProgressEvent (+ BackupSummary sur
// le message final). Pourcentages normalisés 0..100 pour le frontend.
func decodeBackupLine(line string, backupID int64) (domain.ProgressEvent, *domain.BackupSummary) {
	ev := domain.ProgressEvent{BackupID: backupID, Time: time.Now().UTC()}
	var m resticMessage
	if !strings.HasPrefix(strings.TrimSpace(line), "{") || json.Unmarshal([]byte(line), &m) != nil {
		ev.Type = domain.EventLog
		ev.Message = line
		return ev, nil
	}
	switch m.MessageType {
	case "status":
		ev.Type = domain.EventProgress
		ev.Percent = m.PercentDone * 100
		ev.BytesDone = m.BytesDone
		ev.TotalBytes = m.TotalBytes
		ev.FilesDone = m.FilesDone
		ev.TotalFiles = m.TotalFiles
		return ev, nil
	case "summary":
		sum := &domain.BackupSummary{
			SnapshotID:      m.SnapshotID,
			FilesNew:        m.FilesNew,
			FilesChanged:    m.FilesChanged,
			FilesUnmodified: m.FilesUnmodified,
			BytesAdded:      m.DataAdded,
			BytesProcessed:  m.TotalBytesProcessed,
			Duration:        time.Duration(m.TotalDuration * float64(time.Second)),
		}
		ev.Type = domain.EventSummary
		ev.Percent = 100
		ev.BytesDone = m.TotalBytesProcessed
		ev.TotalBytes = m.TotalBytesProcessed
		ev.SnapshotID = m.SnapshotID
		return ev, sum
	case "error":
		ev.Type = domain.EventError
		msg := m.Error.Message
		if m.Item != "" {
			msg += " (" + m.Item + ")"
		}
		if m.During != "" {
			msg += " during " + m.During
		}
		ev.Message = msg
		return ev, nil
	default:
		ev.Type = domain.EventLog
		ev.Message = line
		return ev, nil
	}
}

// decodeRestoreLine : pendant restore de decodeBackupLine (compteurs
// files_restored/bytes_restored ; le restore_id est estampillé plus haut).
func decodeRestoreLine(line string) domain.ProgressEvent {
	ev := domain.ProgressEvent{Time: time.Now().UTC()}
	var m resticMessage
	if !strings.HasPrefix(strings.TrimSpace(line), "{") || json.Unmarshal([]byte(line), &m) != nil {
		ev.Type = domain.EventLog
		ev.Message = line
		return ev
	}
	switch m.MessageType {
	case "status":
		ev.Type = domain.EventProgress
		ev.Percent = m.PercentDone * 100
		ev.BytesDone = m.BytesRestored
		ev.TotalBytes = m.TotalBytes
		ev.FilesDone = m.FilesRestored
		ev.TotalFiles = m.TotalFiles
		return ev
	case "summary":
		ev.Type = domain.EventSummary
		ev.Percent = 100
		ev.BytesDone = m.BytesRestored
		ev.TotalBytes = m.TotalBytes
		ev.FilesDone = m.FilesRestored
		ev.TotalFiles = m.TotalFiles
		return ev
	case "error":
		ev.Type = domain.EventError
		msg := m.Error.Message
		if m.Item != "" {
			msg += " (" + m.Item + ")"
		}
		ev.Message = msg
		return ev
	default:
		ev.Type = domain.EventLog
		ev.Message = line
		return ev
	}
}
