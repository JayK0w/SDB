package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// SchedulerService owns recurring backups: CRUD over schedules plus the
// cron engine firing them. robfig/cron is a pure in-process timing
// library (comparable to time.Ticker), which is why the usecase layer may
// depend on it directly. Expressions use the standard 5-field syntax and
// run in the server's timezone (UTC in the shipped container).
type SchedulerService struct {
	schedules domain.ScheduleRepository
	backups   *BackupService
	logger    *slog.Logger

	mu      sync.Mutex
	cron    *cron.Cron
	entries map[int64]cron.EntryID
}

func NewSchedulerService(schedules domain.ScheduleRepository, backups *BackupService, logger *slog.Logger) *SchedulerService {
	if logger == nil {
		logger = slog.Default()
	}
	return &SchedulerService{
		schedules: schedules,
		backups:   backups,
		logger:    logger,
		cron:      cron.New(),
		entries:   map[int64]cron.EntryID{},
	}
}

// ValidateCron rejects malformed expressions before they are persisted.
func ValidateCron(spec string) error {
	if _, err := cron.ParseStandard(spec); err != nil {
		return fmt.Errorf("%w: invalid cron expression %q: %v", domain.ErrInvalidInput, spec, err)
	}
	return nil
}

// Run loads the schedules, starts the cron engine and blocks until ctx is
// canceled; in-flight fired backups are owned by the BackupService.
func (s *SchedulerService) Run(ctx context.Context) error {
	if err := s.reload(ctx); err != nil {
		return err
	}
	s.cron.Start()
	s.logger.Info("backup scheduler started")
	<-ctx.Done()
	<-s.cron.Stop().Done()
	return nil
}

// reload rebuilds every cron entry from the store; called at startup and
// after each CRUD change (schedules are few, a full rebuild is simpler
// and safer than entry surgery).
func (s *SchedulerService) reload(ctx context.Context) error {
	all, err := s.schedules.List(ctx)
	if err != nil {
		return fmt.Errorf("loading schedules: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entryID := range s.entries {
		s.cron.Remove(entryID)
	}
	s.entries = map[int64]cron.EntryID{}

	for _, sched := range all {
		if !sched.Enabled {
			continue
		}
		sched := sched // capture per iteration
		entryID, err := s.cron.AddFunc(sched.Cron, func() { s.fire(sched) })
		if err != nil {
			// A malformed expression slipped into the store: skip it
			// rather than blocking every other schedule.
			s.logger.Error("skipping schedule with invalid cron expression",
				"schedule", sched.Name, "cron", sched.Cron, "error", err)
			continue
		}
		s.entries[sched.ID] = entryID
	}
	s.logger.Info("schedules loaded", "active", len(s.entries), "total", len(all))
	return nil
}

// fire triggers one scheduled run. A conflict (previous run still going)
// is logged and skipped: overlapping backups of one container are refused
// by design.
func (s *SchedulerService) fire(sched domain.BackupSchedule) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := s.logger.With("schedule", sched.Name, "container", sched.ContainerName)
	rec, err := s.backups.Start(ctx, sched.ToRequest())
	if err != nil {
		log.Error("scheduled backup could not start", "error", err)
		return
	}
	log.Info("scheduled backup fired", "backup_id", rec.ID)
	if err := s.schedules.TouchLastRun(ctx, sched.ID, time.Now().UTC()); err != nil {
		log.Error("recording schedule run", "error", err)
	}
}

// RunNow fires a schedule immediately, outside its cron cadence.
func (s *SchedulerService) RunNow(ctx context.Context, id int64) (*domain.BackupRecord, error) {
	sched, err := s.schedules.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	rec, err := s.backups.Start(ctx, sched.ToRequest())
	if err != nil {
		return nil, err
	}
	if err := s.schedules.TouchLastRun(ctx, id, time.Now().UTC()); err != nil {
		s.logger.Error("recording schedule run", "schedule", sched.Name, "error", err)
	}
	return rec, nil
}

// --- CRUD (validates, persists, then hot-reloads the cron entries) ---

func (s *SchedulerService) Create(ctx context.Context, sched *domain.BackupSchedule) error {
	if err := sched.Validate(); err != nil {
		return err
	}
	if err := ValidateCron(sched.Cron); err != nil {
		return err
	}
	if err := s.schedules.Create(ctx, sched); err != nil {
		return err
	}
	return s.reload(ctx)
}

func (s *SchedulerService) Update(ctx context.Context, sched *domain.BackupSchedule) error {
	if err := sched.Validate(); err != nil {
		return err
	}
	if err := ValidateCron(sched.Cron); err != nil {
		return err
	}
	if err := s.schedules.Update(ctx, sched); err != nil {
		return err
	}
	return s.reload(ctx)
}

func (s *SchedulerService) Delete(ctx context.Context, id int64) error {
	if err := s.schedules.Delete(ctx, id); err != nil {
		return err
	}
	return s.reload(ctx)
}

func (s *SchedulerService) Get(ctx context.Context, id int64) (*domain.BackupSchedule, error) {
	return s.schedules.GetByID(ctx, id)
}

func (s *SchedulerService) List(ctx context.Context) ([]domain.BackupSchedule, error) {
	return s.schedules.List(ctx)
}
