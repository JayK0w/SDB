package sqlite

import (
	"database/sql"
	"strings"
	"time"
)

// timestamps stockés en RFC3339Nano UTC : lisibles, triables, sans perte

func now() string { return fmtTime(time.Now()) }

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

// détection par message : formulation stable de SQLite, plus robuste que
// d'importer les codes internes du driver

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

type rowScanner interface {
	Scan(dest ...any) error
}

func requireAffected(res sql.Result, sentinel error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sentinel
	}
	return nil
}
