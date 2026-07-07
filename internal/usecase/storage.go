package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// StorageService manages Restic repository targets. Creating a storage
// also initialises (or verifies) the repository, so a storage that exists
// in SDB is always usable.
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

// Create persists the configuration and initialises the Restic repository.
// When no repository password is supplied, a strong random one is
// generated (the spec mandates generated passwords; users never have to
// invent one). If the repository cannot be initialised the configuration
// is rolled back so no unusable storage lingers.
func (s *StorageService) Create(ctx context.Context, cfg *domain.StorageConfig) error {
	cfg.ID = 0
	if cfg.ResticPassword == "" {
		pw, err := randomSecret(32) // 43 characters
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

// Update modifies a storage configuration. The repository password is
// immutable: Restic derives the repository keys from it, so swapping it
// here would silently lock the repository out. An empty password in the
// input means "keep the current one".
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

// Delete removes a storage configuration. The repository data itself is
// left untouched; the repository layer returns ErrConflict while backup
// history still references the storage.
func (s *StorageService) Delete(ctx context.Context, id int64) error {
	return s.storages.Delete(ctx, id)
}

// Snapshots lists the snapshots stored in a repository, optionally
// filtered by tags (e.g. "container:postgres").
func (s *StorageService) Snapshots(ctx context.Context, id int64, tags []string) ([]domain.Snapshot, error) {
	cfg, err := s.storages.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.engine.Snapshots(ctx, cfg, tags)
}

// CheckIntegrity runs an on-demand restic check against one repository.
func (s *StorageService) CheckIntegrity(ctx context.Context, id int64) error {
	cfg, err := s.storages.GetByID(ctx, id)
	if err != nil {
		return err
	}
	return s.engine.Check(ctx, cfg)
}
