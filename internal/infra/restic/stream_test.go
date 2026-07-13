package restic

import (
	"strings"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func TestDecodeBackupLineStatus(t *testing.T) {
	line := `{"message_type":"status","percent_done":0.559,"total_files":100,"files_done":50,"total_bytes":1000000,"bytes_done":559000}`
	ev, sum := decodeBackupLine(line, 42)
	if sum != nil {
		t.Fatal("une ligne status ne doit pas produire de summary")
	}
	if ev.Type != domain.EventProgress || ev.BackupID != 42 {
		t.Fatalf("événement inattendu : %+v", ev)
	}
	if ev.Percent < 55.8 || ev.Percent > 56.0 {
		t.Fatalf("Percent = %f, attendu ~55.9 (normalisé 0..100)", ev.Percent)
	}
	if ev.BytesDone != 559000 || ev.TotalBytes != 1000000 || ev.FilesDone != 50 || ev.TotalFiles != 100 {
		t.Fatalf("compteurs mal mappés : %+v", ev)
	}
}

func TestDecodeBackupLineSummary(t *testing.T) {
	line := `{"message_type":"summary","files_new":10,"files_changed":2,"files_unmodified":88,` +
		`"data_added":123456,"total_bytes_processed":1000000,"total_duration":12.5,"snapshot_id":"abcdef1234"}`
	ev, sum := decodeBackupLine(line, 7)
	if sum == nil {
		t.Fatal("une ligne summary doit produire un BackupSummary")
	}
	if sum.SnapshotID != "abcdef1234" || sum.FilesNew != 10 || sum.BytesAdded != 123456 ||
		sum.BytesProcessed != 1000000 {
		t.Fatalf("summary mal mappé : %+v", sum)
	}
	if sum.Duration.Seconds() != 12.5 {
		t.Fatalf("Duration = %v, attendu 12.5s", sum.Duration)
	}
	if ev.Type != domain.EventSummary || ev.Percent != 100 || ev.SnapshotID != "abcdef1234" {
		t.Fatalf("événement inattendu : %+v", ev)
	}
}

func TestDecodeBackupLineError(t *testing.T) {
	line := `{"message_type":"error","error":{"message":"permission denied"},"during":"archival","item":"/sdb/data/pg/x"}`
	ev, sum := decodeBackupLine(line, 1)
	if sum != nil {
		t.Fatal("une ligne error ne doit pas produire de summary")
	}
	if ev.Type != domain.EventError {
		t.Fatalf("Type = %s, attendu error", ev.Type)
	}
	for _, part := range []string{"permission denied", "/sdb/data/pg/x", "archival"} {
		if !strings.Contains(ev.Message, part) {
			t.Errorf("Message %q sans %q", ev.Message, part)
		}
	}
}

func TestDecodeBackupLineNonJSON(t *testing.T) {
	ev, sum := decodeBackupLine("using parent snapshot 1a2b3c", 1)
	if sum != nil || ev.Type != domain.EventLog || ev.Message != "using parent snapshot 1a2b3c" {
		t.Fatalf("ligne brute mal gérée : ev=%+v sum=%v", ev, sum)
	}
}

func TestDecodeRestoreLineStatus(t *testing.T) {
	line := `{"message_type":"status","percent_done":0.25,"total_files":8,"files_restored":2,"total_bytes":4000,"bytes_restored":1000}`
	ev := decodeRestoreLine(line)
	if ev.Type != domain.EventProgress || ev.Percent != 25 || ev.BytesDone != 1000 || ev.FilesDone != 2 {
		t.Fatalf("status restore mal géré : %+v", ev)
	}
}

func TestMountName(t *testing.T) {
	tests := []struct {
		mount domain.Mount
		want  string
	}{
		{domain.Mount{Type: domain.MountVolume, Name: "pgdata"}, "pgdata"},
		{domain.Mount{Type: domain.MountBind, Source: "/srv/app", Destination: "/var/www/html"}, "var-www-html"},
		{domain.Mount{Type: domain.MountBind, Source: "/srv/data"}, "srv-data"},
	}
	for _, tt := range tests {
		if got := mountName(tt.mount); got != tt.want {
			t.Errorf("mountName(%+v) = %q, attendu %q", tt.mount, got, tt.want)
		}
	}
}

func TestDataMountsUniquesEtReadOnly(t *testing.T) {
	mounts := []domain.Mount{
		{Type: domain.MountVolume, Name: "data"},
		{Type: domain.MountBind, Destination: "/data"}, // même nom nettoyé
	}
	workerMounts, paths, err := dataMounts(mounts)
	if err != nil {
		t.Fatalf("dataMounts() : %v", err)
	}
	if len(paths) != 2 || paths[0] == paths[1] {
		t.Fatalf("chemins non uniques : %q", paths)
	}
	for _, m := range workerMounts {
		if !m.ReadOnly {
			t.Fatalf("montage %s pas en lecture seule", m.Destination)
		}
	}
	if _, _, err := dataMounts(nil); err == nil {
		t.Fatal("dataMounts(nil) doit échouer")
	}
}
