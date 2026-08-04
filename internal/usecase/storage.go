package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// StorageService : un stockage présent dans SDB est toujours utilisable —
// la création initialise (ou vérifie) le dépôt restic.
type StorageService struct {
	storages   domain.StorageRepository
	engine     domain.SnapshotEngine
	logger     *slog.Logger
	backfiller CopyBackfiller
}

// CopyBackfiller : rattrapage des snapshots DÉJÀ présents dans le dépôt source
// au moment où une copie secondaire est branchée. Implémenté par
// ReplicationService.
type CopyBackfiller interface {
	Replicate(ctx context.Context, copyID int64) (*ReplicationStatus, error)
}

// StorageOption : réglage optionnel du service, appliqué à la construction.
type StorageOption func(*StorageService)

// WithCopyBackfill : lance la recopie de l'existant dès qu'une copie
// secondaire est créée. Sans lui, brancher une copie sur un dépôt qui contient
// déjà des mois de sauvegardes ne protégerait que les suivantes, et l'écart de
// réplication resterait au maximum jusqu'à la première passe de
// réconciliation.
func WithCopyBackfill(b CopyBackfiller) StorageOption {
	return func(s *StorageService) { s.backfiller = b }
}

func NewStorageService(storages domain.StorageRepository, engine domain.SnapshotEngine,
	logger *slog.Logger, opts ...StorageOption) *StorageService {

	if logger == nil {
		logger = slog.Default()
	}
	s := &StorageService{storages: storages, engine: engine, logger: logger}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create : mot de passe restic généré si absent, dépôt initialisé, et
// rollback de la ligne si l'init échoue (pas de stockage inutilisable).
// minResticPasswordLen : plancher pour un mot de passe fourni par
// l'exploitant. Les mots générés font 43 caractères ; on n'accepte pas
// qu'un choix manuel descende sous ce qui résiste à une attaque hors ligne
// sur un dépôt volé.
const minResticPasswordLen = 20

// Create : initialise le dépôt. Si aucun mot de passe n'est fourni, un
// aléatoire est généré.
//
// Fournir le sien permet de le SÉQUESTRER hors de SDB. C'est ce qui rend la
// perte de sdb.db survivable : sans mot de passe conservé ailleurs, un dépôt
// restic dont SDB détenait seul la clé est définitivement illisible. Après
// création, la valeur n'est plus jamais restituée par l'API — un export
// permanent donnerait à un admin compromis la totalité des dépôts.
func (s *StorageService) Create(ctx context.Context, cfg *domain.StorageConfig) error {
	cfg.ID = 0
	if cfg.ResticPassword == "" {
		pw, err := randomSecret(32) // 43 caractères
		if err != nil {
			return err
		}
		cfg.ResticPassword = pw
	} else if len(cfg.ResticPassword) < minResticPasswordLen {
		return fmt.Errorf("%w: a supplied repository password must be at least %d characters",
			domain.ErrInvalidInput, minResticPasswordLen)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	// une copie secondaire est initialisée DEPUIS sa source, pour hériter de
	// ses paramètres de découpage : c'est irrattrapable après coup
	var source *domain.StorageConfig
	if cfg.IsCopyTarget() {
		var err error
		if source, err = s.copySource(ctx, cfg.CopyOf); err != nil {
			return err
		}
	}
	if err := s.storages.Create(ctx, cfg); err != nil {
		return err
	}

	initErr := s.engine.EnsureRepository(ctx, cfg)
	if source != nil {
		initErr = s.engine.EnsureCopyTarget(ctx, cfg, source)
	}
	if initErr != nil {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if delErr := s.storages.Delete(rbCtx, cfg.ID); delErr != nil {
			s.logger.Error("rolling back unusable storage config", "id", cfg.ID, "error", delErr)
		}
		return fmt.Errorf("initialising restic repository: %w", initErr)
	}
	s.logger.Info("storage created", "name", cfg.Name, "type", cfg.Type, "copy_of", cfg.CopyOf)
	if source != nil {
		s.startBackfill(cfg.ID, cfg.Name, source.Name)
	}
	return nil
}

// backfillTimeout : la recopie initiale d'un dépôt déjà fourni peut durer des
// heures (elle re-téléverse tout, la copie ré-encrypte).
const backfillTimeout = 24 * time.Hour

// startBackfill : recopie l'existant, en tâche de fond.
//
// C'est ce qui rend la copie secondaire activable APRÈS COUP : on branche une
// copie sur un dépôt qui contient déjà l'historique, et cet historique part
// tout de suite au lieu d'attendre la prochaine passe de réconciliation.
//
// Détachée volontairement — la création d'un stockage ne peut pas bloquer
// pendant des heures. Un arrêt de SDB pendant la recopie n'est pas un
// problème : `restic copy` saute ce qui est déjà copié, et la passe de
// réconciliation reprend le reste.
func (s *StorageService) startBackfill(copyID int64, copyName, sourceName string) {
	if s.backfiller == nil {
		return
	}
	log := s.logger.With("copy", copyName, "source", sourceName)
	log.Info("backfilling the secondary copy with existing snapshots")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), backfillTimeout)
		defer cancel()
		st, err := s.backfiller.Replicate(ctx, copyID)
		if err != nil {
			// pas d'échec de création pour autant : le stockage est valide, et
			// la réconciliation retentera
			log.Error("initial backfill failed; the reconciliation pass will retry", "error", err)
			return
		}
		log.Info("initial backfill finished", "copied_snapshots", st.CopiedSnapshots, "pending", st.Pending)
	}()
}

// copySource : valide la source d'une copie secondaire.
//
// Une copie de copie est refusée : les chaînes rendraient l'écart de
// réplication non interprétable (le retard d'un maillon masque celui du
// suivant) et créeraient des cycles possibles. Deux copies d'un même dépôt
// restent parfaitement admises.
func (s *StorageService) copySource(ctx context.Context, id int64) (*domain.StorageConfig, error) {
	src, err := s.storages.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("loading copy source %d: %w", id, err)
	}
	if src.IsCopyTarget() {
		return nil, fmt.Errorf("%w: storage %q is itself a secondary copy; copy chains are not supported",
			domain.ErrInvalidInput, src.Name)
	}
	return src, nil
}

func (s *StorageService) Get(ctx context.Context, id int64) (*domain.StorageConfig, error) {
	return s.storages.GetByID(ctx, id)
}

func (s *StorageService) List(ctx context.Context) ([]domain.StorageConfig, error) {
	return s.storages.List(ctx)
}

// Update : le mot de passe du dépôt est IMMUABLE (restic en dérive ses
// clés — le changer verrouillerait le dépôt) ; vide = conserver l'actuel.
func (s *StorageService) Update(ctx context.Context, cfg *domain.StorageConfig) error {
	existing, err := s.storages.GetByID(ctx, cfg.ID)
	if err != nil {
		return err
	}
	switch cfg.ResticPassword {
	case "", existing.ResticPassword:
		cfg.ResticPassword = existing.ResticPassword
	default:
		return fmt.Errorf("%w: the repository password cannot be changed", domain.ErrInvalidInput)
	}
	// append_only est un cliquet : activable par l'API, jamais désactivable
	// par elle. Sinon un compte admin compromis lèverait la protection puis
	// purgerait le dépôt — exactement le scénario qu'elle doit empêcher.
	// Le retour en arrière est une opération d'exploitation délibérée, hors
	// de la surface d'attaque de l'application.
	if existing.AppendOnly && !cfg.AppendOnly {
		return fmt.Errorf("%w: append-only cannot be disabled through the API", domain.ErrForbidden)
	}
	// Le rattachement d'une copie secondaire est fixé à la création (0 =
	// inchangé, comme le mot de passe). Le rebrancher mélangerait dans un même
	// dépôt les snapshots de deux sources, ce qui rendrait l'écart de
	// réplication ininterprétable des deux côtés ; le détacher laisserait une
	// copie que plus rien ne réconcilie, en la faisant passer pour un dépôt
	// principal sans sauvegarde.
	switch cfg.CopyOf {
	case 0, existing.CopyOf:
		cfg.CopyOf = existing.CopyOf
	default:
		return fmt.Errorf("%w: the copy source of a storage cannot be changed after creation", domain.ErrInvalidInput)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.storages.Update(ctx, cfg)
}

// Delete : le dépôt restic lui-même n'est pas effacé ; ErrConflict si
// l'historique référence encore ce stockage. Refusé sur un dépôt
// append-only : perdre la configuration, c'est perdre le mot de passe du
// dépôt, donc l'accès aux sauvegardes qu'il était censé protéger.
func (s *StorageService) Delete(ctx context.Context, id int64) error {
	cfg, err := s.storages.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := cfg.EnsureMutable("delete"); err != nil {
		return err
	}
	return s.storages.Delete(ctx, id)
}

func (s *StorageService) Snapshots(ctx context.Context, id int64, tags []string) ([]domain.Snapshot, error) {
	cfg, err := s.storages.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.engine.Snapshots(ctx, cfg, tags)
}

func (s *StorageService) CheckIntegrity(ctx context.Context, id int64) error {
	cfg, err := s.storages.GetByID(ctx, id)
	if err != nil {
		return err
	}
	return s.engine.Check(ctx, cfg)
}
