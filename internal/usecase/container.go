package usecase

import (
	"context"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// ContainerService : découverte des conteneurs pour la couche HTTP.
type ContainerService struct {
	runtime domain.ContainerRuntime
}

func NewContainerService(runtime domain.ContainerRuntime) *ContainerService {
	return &ContainerService{runtime: runtime}
}

func (s *ContainerService) List(ctx context.Context, all bool) ([]domain.Container, error) {
	return s.runtime.List(ctx, all)
}

func (s *ContainerService) Get(ctx context.Context, id string) (*domain.Container, error) {
	return s.runtime.Get(ctx, id)
}

// Ping : joignabilité du démon Docker (endpoint /health, North Star).
func (s *ContainerService) Ping(ctx context.Context) error {
	return s.runtime.Ping(ctx)
}
