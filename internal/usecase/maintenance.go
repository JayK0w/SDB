package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// MaintenanceService : vérifications d'intégrité planifiées (restic check)
// sur tous les stockages.
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

// RunChecks : un dépôt cassé n'empêche pas de vérifier les autres.
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

// La planification vit dans MaintenanceScheduler : l'échéance doit survivre
// aux redémarrages, et une seule mécanique la porte pour les trois boucles.
