package usecase

import "github.com/standalone-docker-backup/sdb/internal/domain"

// MultiPublisher fans one event out to several publishers (WebSocket hub,
// metrics collector, ...). Each target must itself be non-blocking, per
// the EventPublisher contract.
type MultiPublisher []domain.EventPublisher

var _ domain.EventPublisher = (MultiPublisher)(nil)

func (m MultiPublisher) Publish(ev domain.ProgressEvent) {
	for _, p := range m {
		p.Publish(ev)
	}
}
