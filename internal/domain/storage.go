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
	// CopyOf : ce dépôt est la COPIE SECONDAIRE du dépôt d'ID indiqué (règle
	// 3-2-1). 0 = dépôt principal, alimenté par des sauvegardes.
	//
	// Le lien est porté par la copie et non par la source : c'est ce qui
	// permet d'initialiser le dépôt secondaire avec les paramètres de
	// découpage de sa source (`restic init --copy-chunker-params`, sans quoi
	// les données copiées peuvent occuper le double), et ça autorise
	// naturellement plusieurs copies d'un même dépôt.
	CopyOf    int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsCopyTarget : dépôt alimenté par réplication, pas par sauvegarde.
func (s *StorageConfig) IsCopyTarget() bool { return s.CopyOf != 0 }

// EnsureBackupTarget : refuse une sauvegarde DIRECTE dans un dépôt de copie.
// L'état de réplication se lit en comparant les snapshots de la source à ceux
// de la copie : des snapshots écrits directement dans la copie rendraient cet
// écart inintelligible, et donc l'alerte « la seconde copie a décroché »
// silencieusement fausse.
func (s *StorageConfig) EnsureBackupTarget() error {
	if s.IsCopyTarget() {
		return fmt.Errorf("%w: storage %q is a secondary copy of storage %d and only receives replicated snapshots",
			ErrInvalidInput, s.Name, s.CopyOf)
	}
	return nil
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
	if s.CopyOf < 0 {
		return fmt.Errorf("%w: copy_of_storage_id must be positive", ErrInvalidInput)
	}
	// une copie de soi-même n'est pas une seconde copie
	if s.CopyOf != 0 && s.CopyOf == s.ID {
		return fmt.Errorf("%w: storage %q cannot be its own secondary copy", ErrInvalidInput, s.Name)
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
