// Package httpapi exposes the delivery layer: the versioned REST API
// (/api/v1, Gin) and the WebSocket hub streaming ProgressEvents to the
// frontend. It translates transport concerns (JSON binding, status codes,
// JWT validation) to and from usecase calls.
package httpapi
