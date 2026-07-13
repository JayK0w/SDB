package domain

import (
	"fmt"
	"time"
)

// BackupSchedule : sauvegarde récurrente. Le conteneur est référencé par
// NOM (les IDs changent quand un conteneur est recréé) et résolu à chaque
// déclenchement.
type BackupSchedule struct {
	ID      int64
	Name    string
	Cron    string // expression cron 5 champs, heure serveur (UTC en conteneur)
	Enabled bool

	ContainerName string
	StorageID     int64
	Volumes       []string
	StopContainer bool
	PreHook       *Hook
	PostHook      *Hook
	Retention     *RetentionPolicy
	Tags          []string

	LastRunAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate : tout sauf l'expression cron (validée par le scheduler qui
// possède le parseur).
func (s *BackupSchedule) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("%w: schedule name is required", ErrInvalidInput)
	}
	if s.Cron == "" {
		return fmt.Errorf("%w: cron expression is required", ErrInvalidInput)
	}
	if s.ContainerName == "" {
		return fmt.Errorf("%w: container name is required", ErrInvalidInput)
	}
	if s.StorageID <= 0 {
		return fmt.Errorf("%w: storage id is required", ErrInvalidInput)
	}
	if s.PreHook != nil {
		if err := s.PreHook.Validate(); err != nil {
			return fmt.Errorf("pre-hook: %w", err)
		}
	}
	if s.PostHook != nil {
		if err := s.PostHook.Validate(); err != nil {
			return fmt.Errorf("post-hook: %w", err)
		}
	}
	if s.Retention != nil {
		if err := s.Retention.Validate(); err != nil {
			return fmt.Errorf("retention: %w", err)
		}
	}
	return nil
}

// ToRequest : requête tirée au déclenchement, taguée du nom de la planif.
func (s *BackupSchedule) ToRequest() BackupRequest {
	return BackupRequest{
		ContainerID:   s.ContainerName, // docker inspect résout aussi les noms
		StorageID:     s.StorageID,
		Volumes:       s.Volumes,
		StopContainer: s.StopContainer,
		PreHook:       s.PreHook,
		PostHook:      s.PostHook,
		Retention:     s.Retention,
		Tags:          append([]string{"scheduled:" + s.Name}, s.Tags...),
	}
}
