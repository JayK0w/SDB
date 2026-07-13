package restic

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/infra/streamio"
)

func (e *Engine) Backup(ctx context.Context, storage *domain.StorageConfig, backupID int64,
	mounts []domain.Mount, tags []string, events chan<- domain.ProgressEvent) (*domain.BackupSummary, error) {

	workerMounts, paths, err := dataMounts(mounts)
	if err != nil {
		return nil, err
	}

	// --host fixe : regroupement stable des snapshots malgré les hostnames
	// aléatoires des workers
	cmd := []string{"backup", "--json", "--host", "sdb"}
	for _, t := range tags {
		cmd = append(cmd, "--tag", t)
	}
	cmd = append(cmd, paths...)

	var summary *domain.BackupSummary
	stdout := &streamio.LineWriter{Emit: func(line string) {
		ev, sum := decodeBackupLine(line, backupID)
		if sum != nil {
			summary = sum
		}
		send(ctx, events, ev)
	}}
	stderrBuf := streamio.NewBounded(32 << 10)
	stderr := &streamio.LineWriter{Emit: func(line string) {
		stderrBuf.Write([]byte(line + "\n"))
		send(ctx, events, domain.ProgressEvent{
			BackupID: backupID,
			Type:     domain.EventError,
			Time:     time.Now().UTC(),
			Message:  line,
		})
	}}

	labels := map[string]string{"sdb.backup_id": strconv.FormatInt(backupID, 10)}
	exit, err := e.run(ctx, storage, cmd, workerMounts, labels, stdout, stderr)
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		return nil, err
	}

	switch {
	case exit == 0 && summary != nil:
		return summary, nil
	case exit == 0:
		return nil, fmt.Errorf("restic exited successfully but produced no summary")
	case exit == 3 && summary != nil:
		// exit 3 : snapshot créé mais fichiers sources illisibles → warning
		return summary, fmt.Errorf("%w: %s", domain.ErrPartial, stderrBuf)
	default:
		return nil, fmt.Errorf("restic backup failed (exit %d): %s", exit, stderrBuf)
	}
}

// Restore : volume cible monté en écriture au MÊME chemin qu'au backup,
// restauration en place avec --include (un snapshot multi-volumes n'écrit
// que dans le volume demandé).
func (e *Engine) Restore(ctx context.Context, storage *domain.StorageConfig,
	snapshotID, targetVolume string, events chan<- domain.ProgressEvent) error {

	if snapshotID == "" || targetVolume == "" {
		return fmt.Errorf("%w: snapshot id and target volume are required", domain.ErrInvalidInput)
	}
	m := domain.Mount{Type: domain.MountVolume, Name: targetVolume}
	m.Destination = dataMountRoot + "/" + mountName(m)
	m.ReadOnly = false

	cmd := []string{"restore", snapshotID, "--json", "--target", "/", "--include", m.Destination}

	stdout := &streamio.LineWriter{Emit: func(line string) {
		send(ctx, events, decodeRestoreLine(line))
	}}
	stderrBuf := streamio.NewBounded(32 << 10)
	stderr := &streamio.LineWriter{Emit: func(line string) {
		stderrBuf.Write([]byte(line + "\n"))
		send(ctx, events, domain.ProgressEvent{
			Type:    domain.EventError,
			Time:    time.Now().UTC(),
			Message: line,
		})
	}}

	exit, err := e.run(ctx, storage, cmd, []domain.Mount{m}, nil, stdout, stderr)
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("restic restore failed (exit %d): %s", exit, stderrBuf)
	}
	return nil
}
