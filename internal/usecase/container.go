package usecase

import (
	"context"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// ContainerService exposes container discovery to the delivery layer, so
// HTTP handlers never touch the runtime port directly.
type ContainerService struct {
	runtime domain.ContainerRuntime
}

func NewContainerService(runtime domain.ContainerRuntime) *ContainerService {
	return &ContainerService{runtime: runtime}
}

func (s *ContainerService) List(ctx context.Context, all bool) ([]domain.Container, error) {
	return s.runtime.List(ctx, all)
}

// Ping reports Docker daemon reachability (health endpoint / North Star).
func (s *ContainerService) Ping(ctx context.Context) error {
	return s.runtime.Ping(ctx)
}

func (s *ContainerService) Get(ctx context.Context, id string) (*domain.Container, error) {
	return s.runtime.Get(ctx, id)
}
