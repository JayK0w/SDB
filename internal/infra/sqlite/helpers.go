package sqlite

import (
	"strings"
	"time"
)

// Timestamps are stored as RFC3339Nano UTC strings: readable in the DB,
// lexicographically sortable, and lossless for Go time values.

func now() string { return fmtTime(time.Now()) }

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// modernc.org/sqlite wraps SQLite constraint failures in plain errors; the
// message substrings below are part of SQLite's stable public wording, so
// matching them is more version-proof than importing driver error codes.

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
