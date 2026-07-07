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

type HistoryRepo struct {
	db *sql.DB
}

var _ domain.BackupHistoryRepository = (*HistoryRepo)(nil)

func NewHistoryRepo(db *sql.DB) *HistoryRepo { return &HistoryRepo{db: db} }

const historyColumns = `id, container_id, container_name, storage_id, status, bytes_processed, snapshot_id, start_time, end_time, error_log`

func (r *HistoryRepo) Create(ctx context.Context, rec *domain.BackupRecord) error {
	if rec.StartTime.IsZero() {
		rec.StartTime = time.Now().UTC()
	}
	if rec.Status == "" {
		rec.Status = domain.BackupPending
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO backups_history (container_id, container_name, storage_id, status, bytes_processed, snapshot_id, start_time, end_time, error_log)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ContainerID, rec.ContainerName, rec.StorageID, string(rec.Status),
		rec.BytesProcessed, rec.SnapshotID, fmtTime(rec.StartTime), nullTime(rec.EndTime), rec.ErrorLog)
	if err != nil {
		return fmt.Errorf("inserting backup record: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	rec.ID = id
	return nil
}

func (r *HistoryRepo) Update(ctx context.Context, rec *domain.BackupRecord) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE backups_history
		 SET container_name = ?, status = ?, bytes_processed = ?, snapshot_id = ?, end_time = ?, error_log = ?
		 WHERE id = ?`,
		rec.ContainerName, string(rec.Status), rec.BytesProcessed, rec.SnapshotID,
		nullTime(rec.EndTime), rec.ErrorLog, rec.ID)
	if err != nil {
		return fmt.Errorf("updating backup record %d: %w", rec.ID, err)
	}
	return requireAffected(res, domain.ErrNotFound)
}

func (r *HistoryRepo) GetByID(ctx context.Context, id int64) (*domain.BackupRecord, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+historyColumns+` FROM backups_history WHERE id = ?`, id)
	return scanRecord(row)
}

func (r *HistoryRepo) List(ctx context.Context, filter domain.HistoryFilter) ([]domain.BackupRecord, error) {
	query := `SELECT ` + historyColumns + ` FROM backups_history`
	var conds []string
	var args []any
	if filter.ContainerID != "" {
		conds = append(conds, "container_id = ?")
		args = append(args, filter.ContainerID)
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
		return nil, fmt.Errorf("listing backup history: %w", err)
	}
	defer rows.Close()

	var out []domain.BackupRecord
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func (r *HistoryRepo) FailInterrupted(ctx context.Context, reason string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE backups_history
		 SET status = ?, end_time = ?,
		     error_log = CASE WHEN error_log = '' THEN ? ELSE error_log || char(10) || ? END
		 WHERE status IN (?, ?)`,
		string(domain.BackupFailed), now(), reason, reason,
		string(domain.BackupPending), string(domain.BackupRunning))
	if err != nil {
		return 0, fmt.Errorf("failing interrupted runs: %w", err)
	}
	return res.RowsAffected()
}

func scanRecord(row rowScanner) (*domain.BackupRecord, error) {
	var rec domain.BackupRecord
	var status, startTime string
	var endTime sql.NullString
	err := row.Scan(&rec.ID, &rec.ContainerID, &rec.ContainerName, &rec.StorageID, &status,
		&rec.BytesProcessed, &rec.SnapshotID, &startTime, &endTime, &rec.ErrorLog)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning backup record: %w", err)
	}
	rec.Status = domain.BackupStatus(status)
	rec.StartTime = parseTime(startTime)
	if endTime.Valid {
		t := parseTime(endTime.String)
		rec.EndTime = &t
	}
	return &rec, nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}
