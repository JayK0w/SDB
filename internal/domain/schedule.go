package domain

import (
	"fmt"
	"time"
)

// BackupSchedule is a recurring backup definition. The target container is
// referenced by name, not ID: containers are routinely recreated (compose
// up, image updates) and keep their name while their ID changes. The name
// is resolved to a live container each time the schedule fires.
type BackupSchedule struct {
	ID      int64
	Name    string
	Cron    string // standard 5-field cron expression, evaluated in server time (UTC in the container)
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

// Validate checks everything except the cron expression, which requires a
// parser and is validated by the scheduler usecase.
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

// ToRequest builds the BackupRequest fired by the scheduler. Runs are
// tagged with the schedule name so snapshots can be traced back to it.
func (s *BackupSchedule) ToRequest() BackupRequest {
	return BackupRequest{
		ContainerID:   s.ContainerName, // docker inspect resolves names too
		StorageID:     s.StorageID,
		Volumes:       s.Volumes,
		StopContainer: s.StopContainer,
		PreHook:       s.PreHook,
		PostHook:      s.PostHook,
		Retention:     s.Retention,
		Tags:          append([]string{"scheduled:" + s.Name}, s.Tags...),
	}
}
