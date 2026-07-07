package domain

import "errors"

// Sentinel errors shared across layers. Infrastructure adapters translate
// technology-specific failures (SQLite, Docker SDK, Restic exit codes) into
// these values so that usecases and the HTTP layer can branch on them
// without importing driver packages.
var (
	ErrNotFound      = errors.New("resource not found")
	ErrAlreadyExists = errors.New("resource already exists")
	ErrInvalidInput  = errors.New("invalid input")
	ErrUnauthorized  = errors.New("authentication required")
	ErrForbidden     = errors.New("operation not allowed")
	ErrConflict      = errors.New("conflicting operation in progress")
	ErrUnavailable   = errors.New("dependency unavailable")
	ErrCanceled      = errors.New("operation canceled")
	// ErrPartial signals an operation that produced a usable result but
	// hit non-fatal problems (e.g. restic exit code 3: snapshot created,
	// some source files unreadable). Runs end in BackupWarning.
	ErrPartial = errors.New("operation completed with warnings")
)
