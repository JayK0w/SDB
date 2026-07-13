package usecase

import "github.com/standalone-docker-backup/sdb/internal/domain"

// MultiPublisher : diffuse un événement vers plusieurs publishers (hub
// WebSocket, collecteur Prometheus...). Chaque cible doit elle-même être
// non bloquante.
type MultiPublisher []domain.EventPublisher

var _ domain.EventPublisher = (MultiPublisher)(nil)

func (m MultiPublisher) Publish(ev domain.ProgressEvent) {
	for _, p := range m {
		p.Publish(ev)
	}
}
