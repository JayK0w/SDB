package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// MaintenanceService runs the scheduled repository integrity checks
// (restic check) across every configured storage.
type MaintenanceService struct {
	storages domain.StorageRepository
	engine   domain.SnapshotEngine
	logger   *slog.Logger
}

func NewMaintenanceService(storages domain.StorageRepository, engine domain.SnapshotEngine, logger *slog.Logger) *MaintenanceService {
	if logger == nil {
		logger = slog.Default()
	}
	return &MaintenanceService{storages: storages, engine: engine, logger: logger}
}

// RunChecks verifies every repository once and returns the joined
// failures; one broken repository does not prevent checking the others.
func (s *MaintenanceService) RunChecks(ctx context.Context) error {
	configs, err := s.storages.List(ctx)
	if err != nil {
		return fmt.Errorf("listing storage configs: %w", err)
	}
	var errs []error
	for i := range configs {
		cfg := &configs[i]
		log := s.logger.With("storage", cfg.Name)
		log.Info("integrity check started")
		start := time.Now()
		if err := s.engine.Check(ctx, cfg); err != nil {
			log.Error("integrity check failed", "error", err)
			errs = append(errs, fmt.Errorf("storage %s: %w", cfg.Name, err))
			continue
		}
		log.Info("integrity check passed", "duration", time.Since(start).Round(time.Second).String())
	}
	return errors.Join(errs...)
}

// Schedule runs RunChecks every interval until ctx is canceled. The first
// check happens after one full interval: piling a repository scan onto
// every process start would hurt more than it helps.
func (s *MaintenanceService) Schedule(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	s.logger.Info("scheduled integrity checks enabled", "interval", interval.String())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.RunChecks(ctx); err != nil {
				s.logger.Error("scheduled integrity checks reported problems", "error", err)
			}
		}
	}
}
