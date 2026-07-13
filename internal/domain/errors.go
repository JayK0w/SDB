// Package domain : entités métier et ports (interfaces). Aucune dépendance
// technique — les couches externes dépendent de lui, jamais l'inverse.
package domain

import "errors"

// Erreurs sentinelles : l'infra traduit ses erreurs techniques vers elles,
// l'API les projette en codes HTTP.
var (
	ErrNotFound      = errors.New("resource not found")
	ErrAlreadyExists = errors.New("resource already exists")
	ErrInvalidInput  = errors.New("invalid input")
	ErrUnauthorized  = errors.New("authentication required")
	ErrForbidden     = errors.New("operation not allowed")
	ErrConflict      = errors.New("conflicting operation in progress")
	ErrUnavailable   = errors.New("dependency unavailable")
	ErrCanceled      = errors.New("operation canceled")
	// ErrPartial : résultat utilisable mais incomplet (restic exit 3)
	// → statut warning.
	ErrPartial = errors.New("operation completed with warnings")
)
