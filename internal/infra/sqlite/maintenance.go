package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// MaintenanceRepo : dates de dernier passage des boucles périodiques.
type MaintenanceRepo struct {
	db *sql.DB
}

var _ domain.MaintenanceStateRepository = (*MaintenanceRepo)(nil)

func NewMaintenanceRepo(db *sql.DB) *MaintenanceRepo { return &MaintenanceRepo{db: db} }

// LastRun : zéro si la tâche n'a jamais tourné — un zéro et une erreur ne se
// confondent pas, l'appelant doit pouvoir traiter « jamais » comme « en
// retard » sans traiter une panne de base comme telle.
func (r *MaintenanceRepo) LastRun(ctx context.Context, task string) (time.Time, error) {
	var at string
	err := r.db.QueryRowContext(ctx, `SELECT last_run_at FROM maintenance_runs WHERE task = ?`, task).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("reading last run of %s: %w", task, err)
	}
	return parseTime(at), nil
}

func (r *MaintenanceRepo) MarkRun(ctx context.Context, task string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO maintenance_runs (task, last_run_at) VALUES (?, ?)
		 ON CONFLICT(task) DO UPDATE SET last_run_at = excluded.last_run_at`,
		task, fmtTime(at))
	if err != nil {
		return fmt.Errorf("recording run of %s: %w", task, err)
	}
	return nil
}
