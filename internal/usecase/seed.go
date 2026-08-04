package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// FreshnessSink : jauges de FRAÎCHEUR réamorçables. Implémenté par le
// collecteur Prometheus ; le usecase ne connaît toujours pas Prometheus.
type FreshnessSink interface {
	SeedLastBackupSuccess(container string, at time.Time)
	SeedLastVerificationSuccess(storage string, at time.Time)
}

// SeedFreshness : réamorce depuis la base les jauges qui répondent « à quand
// remonte la dernière fois que… ».
//
// Ces jauges vivent en mémoire du processus : après un redémarrage elles
// n'existent plus, et une alerte bâtie sur `absent(...)` ou sur l'âge de la
// dernière sauvegarde se déclenche alors qu'il ne s'est rien passé — ou pire,
// se tait parce que la série a disparu. La base, elle, sait depuis quand.
//
// Le compte de runs (`sdb_backups_total`) n'est volontairement PAS réamorcé :
// un compteur Prometheus qui repart de zéro au redémarrage est un cas
// parfaitement géré par `rate()` et `increase()`, alors qu'un compteur
// réamorcé à une valeur arbitraire ferait mentir ces fonctions.
//
// Retourne le nombre de séries réamorcées. Une erreur ici n'a pas à empêcher
// le démarrage : sans réamorçage, les jauges se rempliront au premier
// événement — c'est le comportement d'avant, pas une panne.
func SeedFreshness(
	ctx context.Context,
	history domain.BackupHistoryRepository,
	restores domain.RestoreHistoryRepository,
	storages domain.StorageRepository,
	sink FreshnessSink,
) (int, error) {
	if sink == nil {
		return 0, nil
	}
	var seeded int
	var failures []error

	backups, err := history.LastSuccessByContainer(ctx)
	if err != nil {
		failures = append(failures, fmt.Errorf("last successful backups: %w", err))
	}
	for container, at := range backups {
		sink.SeedLastBackupSuccess(container, at)
		seeded++
	}

	verifications, err := restores.LastVerificationByStorage(ctx)
	if err != nil {
		failures = append(failures, fmt.Errorf("last verification restores: %w", err))
	}
	if len(verifications) > 0 {
		// la jauge est étiquetée par NOM de dépôt, l'historique porte son id
		configs, err := storages.List(ctx)
		if err != nil {
			failures = append(failures, fmt.Errorf("resolving storage names: %w", err))
		}
		names := make(map[int64]string, len(configs))
		for _, cfg := range configs {
			names[cfg.ID] = cfg.Name
		}
		for id, at := range verifications {
			name, ok := names[id]
			if !ok {
				// dépôt supprimé depuis : réamorcer sous son id serait une
				// série orpheline que rien ne viendra plus mettre à jour
				continue
			}
			sink.SeedLastVerificationSuccess(name, at)
			seeded++
		}
	}

	return seeded, errors.Join(failures...)
}
