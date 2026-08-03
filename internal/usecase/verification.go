package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// VerificationService : prouve périodiquement qu'une sauvegarde est
// RESTAURABLE, et pas seulement présente.
//
// `restic check` valide la structure du dépôt, `--read-data-subset` relit une
// fraction des blobs : ni l'un ni l'autre ne prouve que les fichiers se
// matérialisent réellement. Seule une restauration réelle le fait. On extrait
// donc le dernier snapshot dans un volume jetable, avec `restore --verify`
// (restic recompare les empreintes écrites au snapshot), puis on jette le
// volume.
//
// L'exécution est enregistrée dans l'historique des restaurations, attribuée
// à `system:verification` : c'est une vraie restauration, elle doit être
// auditable comme telle, et son échec passe par le même canal d'alerte que
// n'importe quel run raté.
type VerificationService struct {
	containers domain.ContainerRuntime
	engine     domain.SnapshotEngine
	storages   domain.StorageRepository
	history    domain.RestoreHistoryRepository
	publisher  domain.EventPublisher
	logger     *slog.Logger
	observer   func(VerificationResult)

	mu      sync.Mutex
	running bool
}

// VerificationResult : ce qu'une vérification vient de démontrer. La DURÉE est
// la donnée qui manquait pour parler de RTO autrement qu'en promesse : c'est
// une restauration réelle, chronométrée, pas une estimation.
type VerificationResult struct {
	StorageID   int64
	StorageName string
	SnapshotID  string
	Duration    time.Duration
	Succeeded   bool
}

// VerificationOption : réglage optionnel du service, appliqué à la construction.
type VerificationOption func(*VerificationService)

// WithVerificationObserver : appelé après chaque vérification terminée. Sert au
// collecteur Prometheus, que le usecase n'a pas à connaître.
func WithVerificationObserver(fn func(VerificationResult)) VerificationOption {
	return func(s *VerificationService) { s.observer = fn }
}

func NewVerificationService(
	containers domain.ContainerRuntime,
	engine domain.SnapshotEngine,
	storages domain.StorageRepository,
	history domain.RestoreHistoryRepository,
	publisher domain.EventPublisher,
	logger *slog.Logger,
	opts ...VerificationOption,
) *VerificationService {
	if logger == nil {
		logger = slog.Default()
	}
	s := &VerificationService{
		containers: containers, engine: engine, storages: storages,
		history: history, publisher: publisher, logger: logger,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// VerifyStorage : vérifie le dernier snapshot d'un dépôt. Bloquant — appelé
// par le planificateur de maintenance, jamais depuis une requête HTTP.
func (s *VerificationService) VerifyStorage(ctx context.Context, storageID int64) (*domain.RestoreRecord, error) {
	storage, err := s.storages.GetByID(ctx, storageID)
	if err != nil {
		return nil, fmt.Errorf("loading storage config: %w", err)
	}

	snapshots, err := s.engine.Snapshots(ctx, storage, nil)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		// pas de snapshot = rien à prouver, et surtout pas un échec
		s.logger.Info("verification skipped, repository has no snapshot", "storage", storage.Name)
		return nil, nil
	}
	latest := latestSnapshot(snapshots)

	source, err := archivedVolume(latest)
	if err != nil {
		return nil, fmt.Errorf("storage %s: %w", storage.Name, err)
	}

	scratch := domain.VerifyVolumePrefix + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	rec := &domain.RestoreRecord{
		StorageID:    storage.ID,
		SnapshotID:   latest.ID,
		SourceVolume: source,
		TargetVolume: scratch,
		Status:       domain.BackupPending,
		TriggeredBy:  domain.SystemActor("verification"),
		StartTime:    time.Now().UTC(),
	}
	if err := s.history.Create(ctx, rec); err != nil {
		return nil, fmt.Errorf("recording verification run: %w", err)
	}

	log := s.logger.With("storage", storage.Name, "snapshot", latest.ShortID,
		"volume", source, "restore_id", rec.ID)
	log.Info("verification restore started")
	s.transition(ctx, rec, domain.BackupRunning,
		fmt.Sprintf("verifying snapshot %s of volume %s", latest.ShortID, source))

	events := make(chan domain.ProgressEvent, 64)
	var fwd sync.WaitGroup
	fwd.Add(1)
	go func() {
		defer fwd.Done()
		for ev := range events {
			ev.RestoreID = rec.ID
			ev.BackupID = 0
			s.publisher.Publish(ev)
		}
	}()
	verifyErr := s.engine.Restore(ctx, storage, domain.RestoreSpec{
		SnapshotID:   latest.ID,
		SourceVolume: source,
		TargetVolume: scratch,
		Verify:       true,
	}, events)
	close(events)
	fwd.Wait()

	// Le jetable part quoi qu'il arrive, y compris sur annulation : sinon
	// chaque échec laisse un volume orphelin sur l'hôte.
	cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	if cerr := s.containers.RemoveVolume(cleanCtx, scratch); cerr != nil {
		log.Warn("scratch volume left behind", "volume", scratch, "error", cerr)
	}
	cancel()

	s.finish(ctx, rec, verifyErr, log)
	if s.observer != nil {
		s.observer(VerificationResult{
			StorageID: storage.ID, StorageName: storage.Name, SnapshotID: latest.ID,
			// mesurée sur les horodatages persistés : c'est la durée que
			// l'exploitant relira dans l'historique
			Duration:  rec.EndTime.Sub(rec.StartTime),
			Succeeded: verifyErr == nil,
		})
	}
	if verifyErr != nil {
		return rec, verifyErr
	}
	return rec, nil
}

// VerifyAll : passe sur tous les dépôts. Un dépôt en échec n'interrompt pas
// les suivants — une panne isolée ne doit pas masquer l'état des autres.
func (s *VerificationService) VerifyAll(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("%w: a verification pass is already running", domain.ErrConflict)
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	configs, err := s.storages.List(ctx)
	if err != nil {
		return fmt.Errorf("listing storage configs: %w", err)
	}
	var failures []error
	for _, cfg := range configs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.VerifyStorage(ctx, cfg.ID); err != nil {
			s.logger.Error("verification failed", "storage", cfg.Name, "error", err)
			failures = append(failures, fmt.Errorf("%s: %w", cfg.Name, err))
		}
	}
	return errors.Join(failures...)
}

// Schedule : boucle de vérification. Premier passage après un intervalle
// complet — au démarrage, l'hôte a mieux à faire que relire un dépôt.
func (s *VerificationService) Schedule(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	s.logger.Info("restore verification enabled", "interval", every.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.VerifyAll(ctx); err != nil {
				s.logger.Error("verification pass finished with failures", "error", err)
			}
		}
	}
}

func (s *VerificationService) transition(ctx context.Context, rec *domain.RestoreRecord, status domain.BackupStatus, msg string) {
	rec.Status = status
	if err := s.history.Update(ctx, rec); err != nil {
		s.logger.Error("persisting verification status", "restore_id", rec.ID, "error", err)
	}
	s.publisher.Publish(domain.ProgressEvent{
		RestoreID: rec.ID, Type: domain.EventStatus,
		Time: time.Now().UTC(), Status: status, Message: msg,
	})
}

func (s *VerificationService) finish(ctx context.Context, rec *domain.RestoreRecord, err error, log *slog.Logger) {
	now := time.Now().UTC()
	rec.EndTime = &now
	switch {
	case err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrCanceled)):
		rec.Status = domain.BackupCanceled
	case err != nil:
		rec.Status = domain.BackupFailed
		rec.ErrorLog = err.Error()
	default:
		rec.Status = domain.BackupSuccess
	}

	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if uerr := s.history.Update(saveCtx, rec); uerr != nil {
		s.logger.Error("persisting terminal verification status", "restore_id", rec.ID, "error", uerr)
	}
	s.publisher.Publish(domain.ProgressEvent{
		RestoreID: rec.ID, Type: domain.EventStatus,
		Time: now, Status: rec.Status, Message: rec.ErrorLog,
	})

	if rec.Status == domain.BackupSuccess {
		log.Info("verification restore succeeded: backup is provably restorable")
	} else {
		log.Error("verification restore failed", "status", rec.Status, "error", rec.ErrorLog)
	}
}

func latestSnapshot(snaps []domain.Snapshot) domain.Snapshot {
	latest := snaps[0]
	for _, s := range snaps[1:] {
		if s.Time.After(latest.Time) {
			latest = s
		}
	}
	return latest
}

// archivedVolume : retrouve le nom du volume tel qu'archivé à partir des
// chemins du snapshot (`/sdb/data/<nom>`). C'est cette valeur qui pilote le
// --include de la restauration.
func archivedVolume(snap domain.Snapshot) (string, error) {
	const root = "/sdb/data/"
	for _, p := range snap.Paths {
		if len(p) > len(root) && p[:len(root)] == root {
			name := p[len(root):]
			if name != "" {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("%w: snapshot %s has no %s* path to verify",
		domain.ErrInvalidInput, snap.ShortID, root)
}
