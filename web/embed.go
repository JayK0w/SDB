// Package web embeds the built frontend (web/dist) into the SDB binary,
// so the production image ships a single static executable.
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

// The all: prefix keeps dotfiles (web/dist/.gitkeep guarantees the
// directory exists even before the first frontend build).
//
//go:embed all:dist
var dist embed.FS

// Dist returns the built frontend rooted at index.html. It fails when the
// frontend has not been built yet, in which case the server runs API-only.
func Dist() (fs.FS, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("frontend not built (web/dist/index.html missing): %w", err)
	}
	return sub, nil
}
