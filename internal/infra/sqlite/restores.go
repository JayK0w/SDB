package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

type RestoreRepo struct {
	db *sql.DB
}

var _ domain.RestoreHistoryRepository = (*RestoreRepo)(nil)

func NewRestoreRepo(db *sql.DB) *RestoreRepo { return &RestoreRepo{db: db} }

const restoreColumns = `id, storage_id, snapshot_id, target_volume, container_id, container_name, status, start_time, end_time, error_log`

func (r *RestoreRepo) Create(ctx context.Context, rec *domain.RestoreRecord) error {
	if rec.StartTime.IsZero() {
		rec.StartTime = time.Now().UTC()
	}
	if rec.Status == "" {
		rec.Status = domain.BackupPending
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO restores_history (storage_id, snapshot_id, target_volume, container_id, container_name, status, start_time, end_time, error_log)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.StorageID, rec.SnapshotID, rec.TargetVolume, rec.ContainerID, rec.ContainerName,
		string(rec.Status), fmtTime(rec.StartTime), nullTime(rec.EndTime), rec.ErrorLog)
	if err != nil {
		return fmt.Errorf("inserting restore record: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	rec.ID = id
	return nil
}

func (r *RestoreRepo) Update(ctx context.Context, rec *domain.RestoreRecord) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE restores_history
		 SET container_name = ?, status = ?, end_time = ?, error_log = ?
		 WHERE id = ?`,
		rec.ContainerName, string(rec.Status), nullTime(rec.EndTime), rec.ErrorLog, rec.ID)
	if err != nil {
		return fmt.Errorf("updating restore record %d: %w", rec.ID, err)
	}
	return requireAffected(res, domain.ErrNotFound)
}

func (r *RestoreRepo) GetByID(ctx context.Context, id int64) (*domain.RestoreRecord, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+restoreColumns+` FROM restores_history WHERE id = ?`, id)
	return scanRestore(row)
}

func (r *RestoreRepo) List(ctx context.Context, filter domain.RestoreFilter) ([]domain.RestoreRecord, error) {
	query := `SELECT ` + restoreColumns + ` FROM restores_history`
	var conds []string
	var args []any
	if filter.TargetVolume != "" {
		conds = append(conds, "target_volume = ?")
		args = append(args, filter.TargetVolume)
	}
	if filter.StorageID > 0 {
		conds = append(conds, "storage_id = ?")
		args = append(args, filter.StorageID)
	}
	if filter.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, string(filter.Status))
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY start_time DESC, id DESC LIMIT ? OFFSET ?"
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing restore history: %w", err)
	}
	defer rows.Close()

	var out []domain.RestoreRecord
	for rows.Next() {
		rec, err := scanRestore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func (r *RestoreRepo) FailInterrupted(ctx context.Context, reason string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE restores_history
		 SET status = ?, end_time = ?,
		     error_log = CASE WHEN error_log = '' THEN ? ELSE error_log || char(10) || ? END
		 WHERE status IN (?, ?)`,
		string(domain.BackupFailed), now(), reason, reason,
		string(domain.BackupPending), string(domain.BackupRunning))
	if err != nil {
		return 0, fmt.Errorf("failing interrupted restores: %w", err)
	}
	return res.RowsAffected()
}

func scanRestore(row rowScanner) (*domain.RestoreRecord, error) {
	var rec domain.RestoreRecord
	var status, startTime string
	var endTime sql.NullString
	err := row.Scan(&rec.ID, &rec.StorageID, &rec.SnapshotID, &rec.TargetVolume,
		&rec.ContainerID, &rec.ContainerName, &status, &startTime, &endTime, &rec.ErrorLog)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning restore record: %w", err)
	}
	rec.Status = domain.BackupStatus(status)
	rec.StartTime = parseTime(startTime)
	if endTime.Valid {
		t := parseTime(endTime.String)
		rec.EndTime = &t
	}
	return &rec, nil
}
