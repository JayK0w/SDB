package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// StorageService : un stockage présent dans SDB est toujours utilisable —
// la création initialise (ou vérifie) le dépôt restic.
type StorageService struct {
	storages domain.StorageRepository
	engine   domain.SnapshotEngine
	logger   *slog.Logger
}

func NewStorageService(storages domain.StorageRepository, engine domain.SnapshotEngine, logger *slog.Logger) *StorageService {
	if logger == nil {
		logger = slog.Default()
	}
	return &StorageService{storages: storages, engine: engine, logger: logger}
}

// Create : mot de passe restic généré si absent, dépôt initialisé, et
// rollback de la ligne si l'init échoue (pas de stockage inutilisable).
func (s *StorageService) Create(ctx context.Context, cfg *domain.StorageConfig) error {
	cfg.ID = 0
	if cfg.ResticPassword == "" {
		pw, err := randomSecret(32) // 43 caractères
		if err != nil {
			return err
		}
		cfg.ResticPassword = pw
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := s.storages.Create(ctx, cfg); err != nil {
		return err
	}
	if err := s.engine.EnsureRepository(ctx, cfg); err != nil {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if delErr := s.storages.Delete(rbCtx, cfg.ID); delErr != nil {
			s.logger.Error("rolling back unusable storage config", "id", cfg.ID, "error", delErr)
		}
		return fmt.Errorf("initialising restic repository: %w", err)
	}
	s.logger.Info("storage created", "name", cfg.Name, "type", cfg.Type)
	return nil
}

func (s *StorageService) Get(ctx context.Context, id int64) (*domain.StorageConfig, error) {
	return s.storages.GetByID(ctx, id)
}

func (s *StorageService) List(ctx context.Context) ([]domain.StorageConfig, error) {
	return s.storages.List(ctx)
}

// Update : le mot de passe du dépôt est IMMUABLE (restic en dérive ses
// clés — le changer verrouillerait le dépôt) ; vide = conserver l'actuel.
func (s *StorageService) Update(ctx context.Context, cfg *domain.StorageConfig) error {
	existing, err := s.storages.GetByID(ctx, cfg.ID)
	if err != nil {
		return err
	}
	switch cfg.ResticPassword {
	case "", existing.ResticPassword:
		cfg.ResticPassword = existing.ResticPassword
	default:
		return fmt.Errorf("%w: the repository password cannot be changed", domain.ErrInvalidInput)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.storages.Update(ctx, cfg)
}

// Delete : le dépôt restic lui-même n'est pas effacé ; ErrConflict si
// l'historique référence encore ce stockage.
func (s *StorageService) Delete(ctx context.Context, id int64) error {
	return s.storages.Delete(ctx, id)
}

func (s *StorageService) Snapshots(ctx context.Context, id int64, tags []string) ([]domain.Snapshot, error) {
	cfg, err := s.storages.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.engine.Snapshots(ctx, cfg, tags)
}

func (s *StorageService) CheckIntegrity(ctx context.Context, id int64) error {
	cfg, err := s.storages.GetByID(ctx, id)
	if err != nil {
		return err
	}
	return s.engine.Check(ctx, cfg)
}
