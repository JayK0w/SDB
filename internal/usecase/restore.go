package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
// volume. Progress is streamed through the EventPublisher; restores are
// not persisted to backups_history (that table records backups only, per
// the data model).
type RestoreService struct {
	containers domain.ContainerRuntime
	engine     domain.SnapshotEngine
	storages   domain.StorageRepository
	publisher  domain.EventPublisher
	logger     *slog.Logger

	mu   sync.Mutex
	busy map[string]context.CancelFunc // keyed by target volume
	wg   sync.WaitGroup
}

func NewRestoreService(
	containers domain.ContainerRuntime,
	engine domain.SnapshotEngine,
	storages domain.StorageRepository,
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
		publisher:  publisher,
		logger:     logger,
		busy:       map[string]context.CancelFunc{},
	}
}

// Start validates the request and launches the restore asynchronously.
func (s *RestoreService) Start(ctx context.Context, req RestoreRequest) error {
	if req.SnapshotID == "" || req.TargetVolume == "" {
		return fmt.Errorf("%w: snapshot id and target volume are required", domain.ErrInvalidInput)
	}
	storage, err := s.storages.GetByID(ctx, req.StorageID)
	if err != nil {
		return fmt.Errorf("loading storage config: %w", err)
	}
	var target *domain.Container
	if req.StopContainer != "" {
		if target, err = s.containers.Get(ctx, req.StopContainer); err != nil {
			return fmt.Errorf("inspecting container: %w", err)
		}
	}

	s.mu.Lock()
	if _, busy := s.busy[req.TargetVolume]; busy {
		s.mu.Unlock()
		return fmt.Errorf("%w: a restore into volume %s is already running", domain.ErrConflict, req.TargetVolume)
	}
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.busy[req.TargetVolume] = cancel
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
		s.execute(jobCtx, storage, target, req)
	}()
	return nil
}

// Close cancels every running restore and waits for completion, bounded
// by ctx.
func (s *RestoreService) Close(ctx context.Context) error {
	s.mu.Lock()
	for _, cancel := range s.busy {
		cancel()
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

func (s *RestoreService) execute(ctx context.Context, storage *domain.StorageConfig, target *domain.Container, req RestoreRequest) {
	log := s.logger.With("volume", req.TargetVolume, "snapshot", req.SnapshotID, "storage", storage.Name)
	log.Info("restore started")
	s.publish(domain.EventLog, fmt.Sprintf("restore of volume %s from snapshot %s started", req.TargetVolume, req.SnapshotID))

	stoppedByUs := false
	if target != nil && target.IsRunning() {
		if err := s.containers.Stop(ctx, target.ID, 0); err != nil {
			s.fail(log, fmt.Errorf("stopping container: %w", err))
			return
		}
		stoppedByUs = true
		s.publish(domain.EventLog, "container "+target.Name+" stopped for restore")
	}

	events := make(chan domain.ProgressEvent, 64)
	var fwd sync.WaitGroup
	fwd.Add(1)
	go func() {
		defer fwd.Done()
		for ev := range events {
			s.publisher.Publish(ev)
		}
	}()
	restoreErr := s.engine.Restore(ctx, storage, req.SnapshotID, req.TargetVolume, events)
	close(events)
	fwd.Wait()

	if stoppedByUs {
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		if err := s.containers.Start(restartCtx, target.ID); err != nil {
			restoreErr = errors.Join(restoreErr,
				fmt.Errorf("CRITICAL: container %s could not be restarted after restore: %w", target.Name, err))
		} else {
			s.publish(domain.EventLog, "container "+target.Name+" restarted")
		}
		cancel()
	}

	if restoreErr != nil {
		s.fail(log, restoreErr)
		return
	}
	log.Info("restore finished")
	s.publish(domain.EventLog, fmt.Sprintf("restore of volume %s completed", req.TargetVolume))
}

func (s *RestoreService) publish(typ domain.EventType, msg string) {
	s.publisher.Publish(domain.ProgressEvent{Type: typ, Time: time.Now().UTC(), Message: msg})
}

func (s *RestoreService) fail(log *slog.Logger, err error) {
	log.Error("restore failed", "error", err)
	s.publish(domain.EventError, "restore failed: "+err.Error())
}
