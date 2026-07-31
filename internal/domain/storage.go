package domain

import (
	"fmt"
	"time"
)

type StorageType string

const (
	StorageLocal StorageType = "local"
	StorageS3    StorageType = "s3"
	StorageSFTP  StorageType = "sftp"
	StorageREST  StorageType = "rest"
	StorageB2    StorageType = "b2"
	StorageAzure StorageType = "azure"
	StorageGCS   StorageType = "gs"
)

func (t StorageType) Valid() bool {
	switch t {
	case StorageLocal, StorageS3, StorageSFTP, StorageREST, StorageB2, StorageAzure, StorageGCS:
		return true
	}
	return false
}

// StorageConfig : cible de dépôt restic.
// Credentials et ResticPassword sont chiffrés au repos (AES-256-GCM) et
// déchiffrés seulement en mémoire — chiffrés et non hashés car restic a
// besoin du mot de passe en clair pour ouvrir le dépôt.
type StorageConfig struct {
	ID             int64
	Name           string
	Type           StorageType
	Endpoint       string            // chemin (local) ou URL (s3/sftp/rest/b2/azure/gs)
	Credentials    map[string]string // secrets du backend (AWS_*, B2_*, clé SSH...)
	ResticPassword string            // généré par SDB, protège le dépôt
	// AppendOnly : SDB refuse toute opération destructrice sur ce dépôt
	// (forget, prune, suppression de la cible). SDB détient à la fois le
	// socket Docker et le mot de passe du dépôt : sans ce garde-fou, une
	// erreur de configuration de rétention — ou sa compromission — efface
	// les sauvegardes en même temps que la production. Le verrou applicatif
	// ne remplace PAS l'immuabilité côté serveur (rest-server --append-only,
	// S3 Object Lock) : il la complète en supprimant SDB comme vecteur.
	AppendOnly bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// EnsureMutable : garde-fou avant toute opération destructrice.
func (s *StorageConfig) EnsureMutable(op string) error {
	if s.AppendOnly {
		return fmt.Errorf("%w: %s refused, storage %q is append-only", ErrForbidden, op, s.Name)
	}
	return nil
}

func (s *StorageConfig) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("%w: storage name is required", ErrInvalidInput)
	}
	if !s.Type.Valid() {
		return fmt.Errorf("%w: unknown storage type %q", ErrInvalidInput, s.Type)
	}
	if s.Endpoint == "" {
		return fmt.Errorf("%w: storage endpoint is required", ErrInvalidInput)
	}
	if s.ResticPassword == "" {
		return fmt.Errorf("%w: restic repository password is required", ErrInvalidInput)
	}
	return nil
}

// Redacted : copie exposable par l'API — valeurs secrètes vidées,
// seules les clés restent visibles.
func (s *StorageConfig) Redacted() StorageConfig {
	out := *s
	out.ResticPassword = ""
	out.Credentials = make(map[string]string, len(s.Credentials))
	for k := range s.Credentials {
		out.Credentials[k] = ""
	}
	return out
}
