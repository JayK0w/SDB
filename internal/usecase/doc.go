// Package usecase contains the application business rules. Usecases
// orchestrate the domain ports (repositories, container runtime, snapshot
// engine, event publisher) and hold no reference to Gin, SQLite, Docker or
// Restic types.
package usecase
