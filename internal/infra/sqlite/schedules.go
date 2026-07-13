package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

type ScheduleRepo struct {
	db *sql.DB
}

var _ domain.ScheduleRepository = (*ScheduleRepo)(nil)

func NewScheduleRepo(db *sql.DB) *ScheduleRepo { return &ScheduleRepo{db: db} }

const scheduleColumns = `id, name, cron, enabled, container_name, storage_id, volumes, stop_container,
	pre_hook, post_hook, retention, tags, last_run_at, created_at, updated_at`

// hooks/rétention/volumes/tags stockés en JSON : payloads opaques,
// jamais requêtés champ par champ

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encoding schedule field: %w", err)
	}
	return string(b), nil
}

func nullJSON(v any, isNil bool) (any, error) {
	if isNil {
		return nil, nil
	}
	s, err := marshalJSON(v)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (r *ScheduleRepo) encode(s *domain.BackupSchedule) (volumes, tags string, preHook, postHook, retention any, err error) {
	if volumes, err = marshalJSON(s.Volumes); err != nil {
		return
	}
	if tags, err = marshalJSON(s.Tags); err != nil {
		return
	}
	if preHook, err = nullJSON(s.PreHook, s.PreHook == nil); err != nil {
		return
	}
	if postHook, err = nullJSON(s.PostHook, s.PostHook == nil); err != nil {
		return
	}
	retention, err = nullJSON(s.Retention, s.Retention == nil)
	return
}

func (r *ScheduleRepo) Create(ctx context.Context, s *domain.BackupSchedule) error {
	volumes, tags, preHook, postHook, retention, err := r.encode(s)
	if err != nil {
		return err
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.UpdatedAt = s.CreatedAt
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO backup_schedules
		 (name, cron, enabled, container_name, storage_id, volumes, stop_container, pre_hook, post_hook, retention, tags, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Cron, s.Enabled, s.ContainerName, s.StorageID, volumes, s.StopContainer,
		preHook, postHook, retention, tags, fmtTime(s.CreatedAt), fmtTime(s.UpdatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: schedule %q", domain.ErrAlreadyExists, s.Name)
	}
	if err != nil {
		return fmt.Errorf("inserting schedule: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	s.ID = id
	return nil
}

func (r *ScheduleRepo) GetByID(ctx context.Context, id int64) (*domain.BackupSchedule, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM backup_schedules WHERE id = ?`, id)
	return scanSchedule(row)
}

func (r *ScheduleRepo) List(ctx context.Context) ([]domain.BackupSchedule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM backup_schedules ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing schedules: %w", err)
	}
	defer rows.Close()

	var out []domain.BackupSchedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *ScheduleRepo) Update(ctx context.Context, s *domain.BackupSchedule) error {
	volumes, tags, preHook, postHook, retention, err := r.encode(s)
	if err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()
	res, err := r.db.ExecContext(ctx,
		`UPDATE backup_schedules
		 SET name = ?, cron = ?, enabled = ?, container_name = ?, storage_id = ?, volumes = ?,
		     stop_container = ?, pre_hook = ?, post_hook = ?, retention = ?, tags = ?, updated_at = ?
		 WHERE id = ?`,
		s.Name, s.Cron, s.Enabled, s.ContainerName, s.StorageID, volumes,
		s.StopContainer, preHook, postHook, retention, tags, fmtTime(s.UpdatedAt), s.ID)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: schedule %q", domain.ErrAlreadyExists, s.Name)
	}
	if err != nil {
		return fmt.Errorf("updating schedule %d: %w", s.ID, err)
	}
	return requireAffected(res, domain.ErrNotFound)
}

func (r *ScheduleRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM backup_schedules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting schedule %d: %w", id, err)
	}
	return requireAffected(res, domain.ErrNotFound)
}

func (r *ScheduleRepo) TouchLastRun(ctx context.Context, id int64, at time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE backup_schedules SET last_run_at = ? WHERE id = ?`, fmtTime(at), id)
	if err != nil {
		return fmt.Errorf("recording schedule run %d: %w", id, err)
	}
	return requireAffected(res, domain.ErrNotFound)
}

func scanSchedule(row rowScanner) (*domain.BackupSchedule, error) {
	var s domain.BackupSchedule
	var volumes, tags, createdAt, updatedAt string
	var preHook, postHook, retention, lastRunAt sql.NullString
	err := row.Scan(&s.ID, &s.Name, &s.Cron, &s.Enabled, &s.ContainerName, &s.StorageID,
		&volumes, &s.StopContainer, &preHook, &postHook, &retention, &tags,
		&lastRunAt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning schedule: %w", err)
	}
	if err := json.Unmarshal([]byte(volumes), &s.Volumes); err != nil {
		return nil, fmt.Errorf("decoding schedule %d volumes: %w", s.ID, err)
	}
	if err := json.Unmarshal([]byte(tags), &s.Tags); err != nil {
		return nil, fmt.Errorf("decoding schedule %d tags: %w", s.ID, err)
	}
	if preHook.Valid {
		s.PreHook = &domain.Hook{}
		if err := json.Unmarshal([]byte(preHook.String), s.PreHook); err != nil {
			return nil, fmt.Errorf("decoding schedule %d pre-hook: %w", s.ID, err)
		}
	}
	if postHook.Valid {
		s.PostHook = &domain.Hook{}
		if err := json.Unmarshal([]byte(postHook.String), s.PostHook); err != nil {
			return nil, fmt.Errorf("decoding schedule %d post-hook: %w", s.ID, err)
		}
	}
	if retention.Valid {
		s.Retention = &domain.RetentionPolicy{}
		if err := json.Unmarshal([]byte(retention.String), s.Retention); err != nil {
			return nil, fmt.Errorf("decoding schedule %d retention: %w", s.ID, err)
		}
	}
	if lastRunAt.Valid {
		t := parseTime(lastRunAt.String)
		s.LastRunAt = &t
	}
	s.CreatedAt = parseTime(createdAt)
	s.UpdatedAt = parseTime(updatedAt)
	return &s, nil
}
