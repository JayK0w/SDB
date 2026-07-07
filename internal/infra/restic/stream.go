package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// lineWriter splits an arbitrary byte stream into lines and hands each
// complete non-empty line to emit; flush releases a trailing unterminated
// line. It lets RunWorker's chunked output feed the JSON decoder.
type lineWriter struct {
	emit func(string)
	rem  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.rem = append(w.rem, p...)
	for {
		i := bytes.IndexByte(w.rem, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.rem[:i]), "\r")
		w.rem = w.rem[i+1:]
		if line != "" {
			w.emit(line)
		}
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if len(w.rem) > 0 {
		w.emit(string(w.rem))
		w.rem = nil
	}
}

// send forwards an event, tolerating a nil channel and giving up on
// context cancellation so a stalled consumer cannot wedge the pipeline.
func send(ctx context.Context, events chan<- domain.ProgressEvent, ev domain.ProgressEvent) {
	if events == nil {
		return
	}
	select {
	case events <- ev:
	case <-ctx.Done():
	}
}

// resticMessage is the union of the JSON lines restic emits with --json
// during backup and restore.
type resticMessage struct {
	MessageType string `json:"message_type"`

	// status (backup and restore)
	PercentDone float64 `json:"percent_done"` // 0..1
	TotalFiles  int64   `json:"total_files"`
	FilesDone   int64   `json:"files_done"`
	TotalBytes  int64   `json:"total_bytes"`
	BytesDone   int64   `json:"bytes_done"`
	// restore counters
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

// decodeBackupLine turns one restic backup output line into a
// ProgressEvent and, for the terminal summary message, a BackupSummary.
// Percentages are normalised to 0..100 for the frontend.
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

// decodeRestoreLine is the restore-side twin of decodeBackupLine; restore
// reports files_restored/bytes_restored instead of files_done/bytes_done.
func decodeRestoreLine(line string, backupID int64) domain.ProgressEvent {
	ev := domain.ProgressEvent{BackupID: backupID, Time: time.Now().UTC()}
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

// boundedBuffer keeps at most limit bytes (plus a truncation note), so
// stderr capture cannot grow unbounded.
type boundedBuffer struct {
	limit   int
	buf     []byte
	dropped int
}

func newBoundedBuffer(limit int) *boundedBuffer { return &boundedBuffer{limit: limit} }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.limit - len(b.buf)
	if room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
		b.dropped += len(p) - room
	} else {
		b.dropped += len(p)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	if b.dropped > 0 {
		return string(b.buf) + "\n... (" + strconv.Itoa(b.dropped) + " bytes truncated)"
	}
	return string(b.buf)
}
