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

// BackupService orchestrates the full backup pipeline:
//
//	pre-hook -> optional container stop -> ephemeral restic worker
//	(volumes read-only) -> container restart -> post-hook -> retention
//
// Start returns as soon as the run is accepted (HTTP 202 semantics); the
// pipeline executes in a goroutine tied to its own cancellable context.
// Progress reaches the frontend through the EventPublisher and every state
// change is persisted to backups_history. Whatever goes wrong, a container
// stopped by SDB is restarted (rollback), on a context that survives
// cancellation.
type BackupService struct {
	containers domain.ContainerRuntime
	engine     domain.SnapshotEngine
	storages   domain.StorageRepository
	history    domain.BackupHistoryRepository
	publisher  domain.EventPublisher
	logger     *slog.Logger

	mu      sync.Mutex
	running map[string]*job // keyed by container ID: one run per container
	wg      sync.WaitGroup
}

type job struct {
	backupID int64
	cancel   context.CancelFunc
}

func NewBackupService(
	containers domain.ContainerRuntime,
	engine domain.SnapshotEngine,
	storages domain.StorageRepository,
	history domain.BackupHistoryRepository,
	publisher domain.EventPublisher,
	logger *slog.Logger,
) *BackupService {
	if logger == nil {
		logger = slog.Default()
	}
	return &BackupService{
		containers: containers,
		engine:     engine,
		storages:   storages,
		history:    history,
		publisher:  publisher,
		logger:     logger,
		running:    map[string]*job{},
	}
}

// Start validates the request, records a pending run and launches the
// pipeline asynchronously. It rejects a second run for a container that
// already has one in flight (ErrConflict).
func (s *BackupService) Start(ctx context.Context, req domain.BackupRequest) (*domain.BackupRecord, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	storage, err := s.storages.GetByID(ctx, req.StorageID)
	if err != nil {
		return nil, fmt.Errorf("loading storage config: %w", err)
	}
	target, err := s.containers.Get(ctx, req.ContainerID)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}
	mounts, err := selectMounts(target, req.Volumes)
	if err != nil {
		return nil, err
	}

	rec := &domain.BackupRecord{
		ContainerID:   target.ID,
		ContainerName: target.Name,
		StorageID:     storage.ID,
		Status:        domain.BackupPending,
		StartTime:     time.Now().UTC(),
	}

	s.mu.Lock()
	if _, busy := s.running[target.ID]; busy {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: a backup of container %s is already running", domain.ErrConflict, target.Name)
	}
	if err := s.history.Create(ctx, rec); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("recording backup run: %w", err)
	}
	// The job context is detached from the (short-lived) request context
	// but individually cancellable through Cancel and Close.
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.running[target.ID] = &job{backupID: rec.ID, cancel: cancel}
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			delete(s.running, target.ID)
			s.mu.Unlock()
		}()
		s.execute(jobCtx, rec, target, storage, req, mounts)
	}()
	return rec, nil
}

// Cancel aborts a running backup by its record ID.
func (s *BackupService) Cancel(backupID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.running {
		if j.backupID == backupID {
			j.cancel()
			return nil
		}
	}
	return fmt.Errorf("%w: no running backup with id %d", domain.ErrNotFound, backupID)
}

// Close cancels every running job and waits for their rollback paths to
// complete, bounded by ctx.
func (s *BackupService) Close(ctx context.Context) error {
	s.mu.Lock()
	for _, j := range s.running {
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

func (s *BackupService) History(ctx context.Context, filter domain.HistoryFilter) ([]domain.BackupRecord, error) {
	return s.history.List(ctx, filter)
}

func (s *BackupService) GetRecord(ctx context.Context, id int64) (*domain.BackupRecord, error) {
	return s.history.GetByID(ctx, id)
}

func (s *BackupService) execute(ctx context.Context, rec *domain.BackupRecord, target *domain.Container,
	storage *domain.StorageConfig, req domain.BackupRequest, mounts []domain.Mount) {

	log := s.logger.With("backup_id", rec.ID, "container", target.Name, "storage", storage.Name)
	log.Info("backup started")
	s.transition(ctx, rec, domain.BackupRunning, "backup started")

	var warnings []string

	// 1. Pre-hook. Default policy: abort — snapshotting inconsistent data
	// is worse than not snapshotting at all.
	if req.PreHook != nil {
		warn, err := s.runHook(ctx, target.ID, req.PreHook, domain.HookAbort, "pre-hook", rec.ID)
		if err != nil {
			s.finish(ctx, rec, err, warnings, log)
			return
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// 2. Cold backup: stop the target for the duration of the snapshot.
	stoppedByUs := false
	if req.StopContainer && target.IsRunning() {
		if err := s.containers.Stop(ctx, target.ID, 0); err != nil {
			s.finish(ctx, rec, fmt.Errorf("stopping container: %w", err), warnings, log)
			return
		}
		stoppedByUs = true
		s.event(rec.ID, domain.EventLog, "container stopped for cold backup")
	}

	// 3. Snapshot through the ephemeral worker.
	summary, backupErr := s.snapshot(ctx, rec, storage, target, mounts, req.Tags)
	if errors.Is(backupErr, domain.ErrPartial) {
		warnings = append(warnings, backupErr.Error())
		backupErr = nil
	}
	if summary != nil {
		rec.SnapshotID = summary.SnapshotID
		rec.BytesProcessed = summary.BytesProcessed
	}

	// 4. Rollback / restart. Whatever happened above — engine failure,
	// cancellation — the target container must come back up, so this runs
	// on a context that survives cancellation.
	var restartErr error
	if stoppedByUs {
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		restartErr = s.containers.Start(restartCtx, target.ID)
		cancel()
		if restartErr != nil {
			restartErr = fmt.Errorf("CRITICAL: container %s could not be restarted after backup: %w", target.Name, restartErr)
			log.Error("container restart failed", "error", restartErr)
			s.event(rec.ID, domain.EventError, restartErr.Error())
			backupErr = errors.Join(backupErr, restartErr)
		} else {
			s.event(rec.ID, domain.EventLog, "container restarted")
		}
	}

	// 5. Post-hook. Cleanup should run even after a failed snapshot, as
	// long as the container is up. Default policy: continue — a cleanup
	// failure must not void a good snapshot.
	if req.PostHook != nil && restartErr == nil {
		warn, err := s.runHook(ctx, target.ID, req.PostHook, domain.HookContinue, "post-hook", rec.ID)
		if err != nil {
			backupErr = errors.Join(backupErr, err)
		} else if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// 6. Retention, only after a successful snapshot. A retention failure
	// does not invalidate the backup itself: warning, not failure.
	if backupErr == nil && req.Retention != nil {
		if err := s.engine.Forget(ctx, storage, *req.Retention); err != nil {
			msg := "retention failed: " + err.Error()
			warnings = append(warnings, msg)
			s.event(rec.ID, domain.EventError, msg)
		}
	}

	s.finish(ctx, rec, backupErr, warnings, log)
}

// snapshot ensures the repository exists and runs the backup, forwarding
// engine events to the publisher. Closing the events channel after Backup
// returns is safe because RunWorker guarantees no writes after return.
func (s *BackupService) snapshot(ctx context.Context, rec *domain.BackupRecord, storage *domain.StorageConfig,
	target *domain.Container, mounts []domain.Mount, extraTags []string) (*domain.BackupSummary, error) {

	if err := s.engine.EnsureRepository(ctx, storage); err != nil {
		return nil, fmt.Errorf("preparing repository: %w", err)
	}
	tags := append([]string{"container:" + target.Name}, extraTags...)

	events := make(chan domain.ProgressEvent, 64)
	var fwd sync.WaitGroup
	fwd.Add(1)
	go func() {
		defer fwd.Done()
		for ev := range events {
			s.publisher.Publish(ev)
		}
	}()
	summary, err := s.engine.Backup(ctx, storage, rec.ID, mounts, tags, events)
	close(events)
	fwd.Wait()
	return summary, err
}

// runHook executes a hook inside the target container. On failure it
// either returns a fatal error (abort policy) or a warning message
// (continue policy).
func (s *BackupService) runHook(ctx context.Context, containerID string, hook *domain.Hook,
	def domain.HookFailurePolicy, name string, backupID int64) (warning string, fatal error) {

	s.event(backupID, domain.EventLog, name+": "+strings.Join(hook.Command, " "))
	res, err := s.containers.Exec(ctx, containerID, hook.Command, hook.EffectiveTimeout())

	var failure string
	switch {
	case err != nil:
		failure = fmt.Sprintf("%s failed: %v", name, err)
	case res.ExitCode != 0:
		failure = fmt.Sprintf("%s failed (exit %d): %s", name, res.ExitCode, strings.TrimSpace(res.Stderr))
	default:
		return "", nil
	}
	if hook.EffectiveOnFailure(def) == domain.HookAbort {
		return "", errors.New(failure)
	}
	s.event(backupID, domain.EventError, failure)
	return failure, nil
}

func (s *BackupService) transition(ctx context.Context, rec *domain.BackupRecord, status domain.BackupStatus, msg string) {
	rec.Status = status
	if err := s.history.Update(ctx, rec); err != nil {
		s.logger.Error("persisting backup status", "backup_id", rec.ID, "error", err)
	}
	s.publisher.Publish(domain.ProgressEvent{
		BackupID:  rec.ID,
		Container: rec.ContainerName,
		Type:      domain.EventStatus,
		Time:      time.Now().UTC(),
		Status:    status,
		Message:   msg,
	})
}

func (s *BackupService) event(backupID int64, typ domain.EventType, msg string) {
	s.publisher.Publish(domain.ProgressEvent{
		BackupID: backupID,
		Type:     typ,
		Time:     time.Now().UTC(),
		Message:  msg,
	})
}

// finish classifies the outcome, persists the terminal record (on a
// context that survives cancellation) and publishes the final status.
func (s *BackupService) finish(ctx context.Context, rec *domain.BackupRecord, err error, warnings []string, log *slog.Logger) {
	now := time.Now().UTC()
	rec.EndTime = &now

	switch {
	case err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrCanceled)):
		rec.Status = domain.BackupCanceled
	case err != nil:
		rec.Status = domain.BackupFailed
	case len(warnings) > 0:
		rec.Status = domain.BackupWarning
	default:
		rec.Status = domain.BackupSuccess
	}

	var parts []string
	if err != nil {
		parts = append(parts, err.Error())
	}
	parts = append(parts, warnings...)
	rec.ErrorLog = strings.Join(parts, "\n")

	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if uerr := s.history.Update(saveCtx, rec); uerr != nil {
		s.logger.Error("persisting terminal backup status", "backup_id", rec.ID, "error", uerr)
	}
	s.publisher.Publish(domain.ProgressEvent{
		BackupID:   rec.ID,
		Container:  rec.ContainerName,
		Type:       domain.EventStatus,
		Time:       now,
		Status:     rec.Status,
		Message:    rec.ErrorLog,
		SnapshotID: rec.SnapshotID,
	})

	switch rec.Status {
	case domain.BackupSuccess:
		log.Info("backup finished", "snapshot", rec.SnapshotID, "bytes", rec.BytesProcessed)
	case domain.BackupWarning:
		log.Warn("backup finished with warnings", "snapshot", rec.SnapshotID, "warnings", warnings)
	case domain.BackupCanceled:
		log.Warn("backup canceled")
	default:
		log.Error("backup failed", "error", rec.ErrorLog)
	}
}

// selectMounts resolves the mounts to snapshot: the requested volume
// names, or every backupable mount when no filter is given.
func selectMounts(c *domain.Container, volumes []string) ([]domain.Mount, error) {
	backupable := c.BackupableMounts()
	if len(volumes) == 0 {
		if len(backupable) == 0 {
			return nil, fmt.Errorf("%w: container %s has no backupable volumes", domain.ErrInvalidInput, c.Name)
		}
		return backupable, nil
	}
	byName := map[string]domain.Mount{}
	for _, m := range backupable {
		if m.Name != "" {
			byName[m.Name] = m
		}
	}
	out := make([]domain.Mount, 0, len(volumes))
	for _, v := range volumes {
		m, ok := byName[v]
		if !ok {
			return nil, fmt.Errorf("%w: volume %q is not mounted by container %s", domain.ErrInvalidInput, v, c.Name)
		}
		out = append(out, m)
	}
	return out, nil
}
