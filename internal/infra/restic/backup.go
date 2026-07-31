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

// Restore : le volume CIBLE est monté en écriture au chemin sous lequel le
// volume SOURCE a été archivé, et --include ne retient que ce chemin (un
// snapshot multi-volumes n'écrit que dans le volume demandé). Le chemin
// vient donc de la source, jamais de la cible : c'est ce qui permet de
// restaurer dans un volume au nom différent — sinon --include désignerait
// un chemin absent du snapshot et rien ne serait restauré.
func (e *Engine) Restore(ctx context.Context, storage *domain.StorageConfig,
	spec domain.RestoreSpec, events chan<- domain.ProgressEvent) error {

	if spec.SnapshotID == "" || spec.TargetVolume == "" {
		return fmt.Errorf("%w: snapshot id and target volume are required", domain.ErrInvalidInput)
	}
	archived := dataMountRoot + "/" + mountName(domain.Mount{Type: domain.MountVolume, Name: spec.Source()})
	m := domain.Mount{
		Type:        domain.MountVolume,
		Name:        spec.TargetVolume,
		Destination: archived,
		ReadOnly:    false,
	}

	cmd := []string{"restore", spec.SnapshotID, "--json", "--target", "/", "--include", archived}
	if spec.Verify {
		// restic relit les fichiers écrits et compare aux empreintes du
		// snapshot : une sortie 0 prouve alors la restaurabilité.
		cmd = append(cmd, "--verify")
	}

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
