package domain

import "time"

// RestoreRecord : une ligne de restores_history.
type RestoreRecord struct {
	ID         int64
	StorageID  int64
	SnapshotID string
	// SourceVolume : volume tel qu'il a été archivé dans le snapshot. Vide
	// = restauration en place (source = cible). Différent de TargetVolume
	// = clonage vers un nouveau volume, l'original reste intact.
	SourceVolume  string
	TargetVolume  string
	ContainerID   string // conteneur arrêté pendant la restauration, si demandé
	ContainerName string
	Status        BackupStatus
	TriggeredBy   Actor
	StartTime     time.Time
	EndTime       *time.Time
	ErrorLog      string
}

// IsClone : la restauration écrit dans un volume autre que celui d'origine.
func (r *RestoreRecord) IsClone() bool {
	return r.SourceVolume != "" && r.SourceVolume != r.TargetVolume
}

// RestoreSpec : paramètres d'une extraction de snapshot par le moteur.
// Regroupés en structure plutôt qu'en cascade d'arguments : trois chaînes
// consécutives de même type s'inversent silencieusement à l'appel.
type RestoreSpec struct {
	SnapshotID string
	// SourceVolume : volume tel qu'archivé dans le snapshot. Vide =
	// identique à TargetVolume (restauration en place).
	SourceVolume string
	TargetVolume string
	// Verify : restic recalcule l'empreinte de chaque fichier écrit et
	// échoue si elle diverge du snapshot. Coûteux en lecture, mais c'est la
	// seule façon de prouver qu'une sauvegarde est réellement restaurable
	// plutôt que simplement présente.
	Verify bool
}

// Source : volume d'origine effectif, en repliant le cas « en place ».
func (s RestoreSpec) Source() string {
	if s.SourceVolume == "" {
		return s.TargetVolume
	}
	return s.SourceVolume
}

// RestoreFilter : filtre de restores_history, zéro = ignoré.
type RestoreFilter struct {
	TargetVolume string
	StorageID    int64
	Status       BackupStatus
	Limit        int
	Offset       int
}
