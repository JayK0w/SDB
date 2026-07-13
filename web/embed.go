// Package web : embarque le frontend compilé (web/dist) dans le binaire —
// l'image de production est un seul exécutable statique.
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

// all: inclut les dotfiles (.gitkeep garantit l'existence de dist même
// avant le premier build frontend)
//
//go:embed all:dist
var dist embed.FS

// Dist : frontend compilé. Échec si non construit → le serveur tourne en
// mode API seule.
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
