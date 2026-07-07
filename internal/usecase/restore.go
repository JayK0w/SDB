package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// RestoreRequest describes a restore of one snapshot into one volume.
type RestoreRequest struct {
	StorageID    int64
	SnapshotID   string
	TargetVolume string
	// StopContainer, when set, names the container to stop for the
	// duration of the restore (an application writing to the volume while
	// restic rewrites it would corrupt the result). It is restarted
	// afterwards no matter how the restore ends.
	StopContainer string
}

// RestoreService runs restores asynchronously, one at a time per target
// volume, persists every run to restores_history and streams progress
// through the EventPublisher with the restore id stamped on each event.
type RestoreService struct {
	containers domain.ContainerRuntime
	engine     domain.SnapshotEngine
	storages   domain.StorageRepository
	history    domain.RestoreHistoryRepository
	publisher  domain.EventPublisher
	logger     *slog.Logger

	mu   sync.Mutex
	busy map[string]*restoreJob // keyed by target volume
	wg   sync.WaitGroup
}

type restoreJob struct {
	restoreID int64
	cancel    context.CancelFunc
}

func NewRestoreService(
	containers domain.ContainerRuntime,
	engine domain.SnapshotEngine,
	storages domain.StorageRepository,
	history domain.RestoreHistoryRepository,
	publisher domain.EventPublisher,
	logger *slog.Logger,
) *RestoreService {
	if logger == nil {
		logger = slog.Default()
	}
	return &RestoreService{
		containers: containers,
		engine:     engine,
		storages:   storages,
		history:    history,
		publisher:  publisher,
		logger:     logger,
		busy:       map[string]*restoreJob{},
	}
}

// Start validates the request, records a pending restore and launches it
// asynchronously (HTTP 202 semantics).
func (s *RestoreService) Start(ctx context.Context, req RestoreRequest) (*domain.RestoreRecord, error) {
	if req.SnapshotID == "" || req.TargetVolume == "" {
		return nil, fmt.Errorf("%w: snapshot id and target volume are required", domain.ErrInvalidInput)
	}
	storage, err := s.storages.GetByID(ctx, req.StorageID)
	if err != nil {
		return nil, fmt.Errorf("loading storage config: %w", err)
	}
	var target *domain.Container
	if req.StopContainer != "" {
		if target, err = s.containers.Get(ctx, req.StopContainer); err != nil {
			return nil, fmt.Errorf("inspecting container: %w", err)
		}
	}

	rec := &domain.RestoreRecord{
		StorageID:    storage.ID,
		SnapshotID:   req.SnapshotID,
		TargetVolume: req.TargetVolume,
		Status:       domain.BackupPending,
		StartTime:    time.Now().UTC(),
	}
	if target != nil {
		rec.ContainerID = target.ID
		rec.ContainerName = target.Name
	}

	s.mu.Lock()
	if _, busy := s.busy[req.TargetVolume]; busy {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: a restore into volume %s is already running", domain.ErrConflict, req.TargetVolume)
	}
	if err := s.history.Create(ctx, rec); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("recording restore run: %w", err)
	}
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.busy[req.TargetVolume] = &restoreJob{restoreID: rec.ID, cancel: cancel}
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			delete(s.busy, req.TargetVolume)
			s.mu.Unlock()
		}()
		s.execute(jobCtx, rec, storage, target)
	}()
	return rec, nil
}

// Cancel aborts a running restore by its record ID.
func (s *RestoreService) Cancel(restoreID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.busy {
		if j.restoreID == restoreID {
			j.cancel()
			return nil
		}
	}
	return fmt.Errorf("%w: no running restore with id %d", domain.ErrNotFound, restoreID)
}

// Close cancels every running restore and waits for their rollback paths,
// bounded by ctx.
func (s *RestoreService) Close(ctx context.Context) error {
	s.mu.Lock()
	for _, j := range s.busy {
		j.cancel()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *RestoreService) History(ctx context.Context, filter domain.RestoreFilter) ([]domain.RestoreRecord, error) {
	return s.history.List(ctx, filter)
}

func (s *RestoreService) GetRecord(ctx context.Context, id int64) (*domain.RestoreRecord, error) {
	return s.history.GetByID(ctx, id)
}

func (s *RestoreService) execute(ctx context.Context, rec *domain.RestoreRecord, storage *domain.StorageConfig, target *domain.Container) {
	log := s.logger.With("restore_id", rec.ID, "volume", rec.TargetVolume, "snapshot", rec.SnapshotID, "storage", storage.Name)
	log.Info("restore started")
	s.transition(ctx, rec, domain.BackupRunning,
		fmt.Sprintf("restore of volume %s from snapshot %s started", rec.TargetVolume, rec.SnapshotID))

	stoppedByUs := false
	if target != nil && target.IsRunning() {
		if err := s.containers.Stop(ctx, target.ID, 0); err != nil {
			s.finish(ctx, rec, fmt.Errorf("stopping container: %w", err), log)
			return
		}
		stoppedByUs = true
		s.event(rec, domain.EventLog, "container "+target.Name+" stopped for restore")
	}

	events := make(chan domain.ProgressEvent, 64)
	var fwd sync.WaitGroup
	fwd.Add(1)
	go func() {
		defer fwd.Done()
		for ev := range events {
			ev.RestoreID = rec.ID
			ev.BackupID = 0
			s.publisher.Publish(ev)
		}
	}()
	restoreErr := s.engine.Restore(ctx, storage, rec.SnapshotID, rec.TargetVolume, events)
	close(events)
	fwd.Wait()

	if stoppedByUs {
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		if err := s.containers.Start(restartCtx, target.ID); err != nil {
			restoreErr = errors.Join(restoreErr,
				fmt.Errorf("CRITICAL: container %s could not be restarted after restore: %w", target.Name, err))
		} else {
			s.event(rec, domain.EventLog, "container "+target.Name+" restarted")
		}
		cancel()
	}

	s.finish(ctx, rec, restoreErr, log)
}

func (s *RestoreService) transition(ctx context.Context, rec *domain.RestoreRecord, status domain.BackupStatus, msg string) {
	rec.Status = status
	if err := s.history.Update(ctx, rec); err != nil {
		s.logger.Error("persisting restore status", "restore_id", rec.ID, "error", err)
	}
	s.publisher.Publish(domain.ProgressEvent{
		RestoreID: rec.ID,
		Container: rec.ContainerName,
		Type:      domain.EventStatus,
		Time:      time.Now().UTC(),
		Status:    status,
		Message:   msg,
	})
}

func (s *RestoreService) event(rec *domain.RestoreRecord, typ domain.EventType, msg string) {
	s.publisher.Publish(domain.ProgressEvent{
		RestoreID: rec.ID,
		Container: rec.ContainerName,
		Type:      typ,
		Time:      time.Now().UTC(),
		Message:   msg,
	})
}

func (s *RestoreService) finish(ctx context.Context, rec *domain.RestoreRecord, err error, log *slog.Logger) {
	now := time.Now().UTC()
	rec.EndTime = &now
	switch {
	case err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrCanceled)):
		rec.Status = domain.BackupCanceled
	case err != nil:
		rec.Status = domain.BackupFailed
	default:
		rec.Status = domain.BackupSuccess
	}
	if err != nil {
		rec.ErrorLog = err.Error()
	}

	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if uerr := s.history.Update(saveCtx, rec); uerr != nil {
		s.logger.Error("persisting terminal restore status", "restore_id", rec.ID, "error", uerr)
	}
	s.publisher.Publish(domain.ProgressEvent{
		RestoreID: rec.ID,
		Container: rec.ContainerName,
		Type:      domain.EventStatus,
		Time:      now,
		Status:    rec.Status,
		Message:   strings.TrimSpace(rec.ErrorLog),
	})

	switch rec.Status {
	case domain.BackupSuccess:
		log.Info("restore finished")
	case domain.BackupCanceled:
		log.Warn("restore canceled")
	default:
		log.Error("restore failed", "error", rec.ErrorLog)
	}
}
