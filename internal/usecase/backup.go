// Package usecase : règles métier. Orchestre les ports du domaine sans
// jamais importer Gin, SQLite, Docker ni restic.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// BackupService : pipeline complet d'une sauvegarde —
// pre-hook → arrêt optionnel → worker restic (volumes ro) → redémarrage
// garanti → post-hook → rétention.
// Start rend la main immédiatement (sémantique HTTP 202) ; le run tourne
// dans une goroutine au contexte détaché mais annulable.
type BackupService struct {
	containers domain.ContainerRuntime
	engine     domain.SnapshotEngine
	storages   domain.StorageRepository
	history    domain.BackupHistoryRepository
	publisher  domain.EventPublisher
	logger     *slog.Logger

	// strictPartial : une sauvegarde partielle (restic exit 3, fichiers
	// sources illisibles) est un échec, pas un avertissement. Compter une
	// sauvegarde incomplète comme réussie fait croire à une couverture qui
	// n'existe pas — le pire mode de défaillance pour de la donnée critique.
	strictPartial bool

	// replicator : copie du snapshot vers les dépôts secondaires. nil = aucune
	// copie (le service reste utilisable sans, notamment en test).
	replicator SnapshotReplicator

	mu      sync.Mutex
	running map[string]*job // clé = ID conteneur : un seul run à la fois
	wg      sync.WaitGroup
}

// BackupOption : réglage optionnel du service, appliqué à la construction.
type BackupOption func(*BackupService)

// WithStrictPartial : bascule le traitement des sauvegardes partielles.
// Actif par défaut ; le désactiver est un choix explicite de tolérance.
func WithStrictPartial(strict bool) BackupOption {
	return func(s *BackupService) { s.strictPartial = strict }
}

// SnapshotReplicator : recopie un snapshot vers les copies secondaires de son
// dépôt (règle 3-2-1). Implémenté par ReplicationService.
type SnapshotReplicator interface {
	ReplicateAfterBackup(ctx context.Context, sourceID, backupID int64, snapshotID string) error
}

// WithReplicator : branche la copie secondaire à la fin de chaque sauvegarde
// réussie.
func WithReplicator(r SnapshotReplicator) BackupOption {
	return func(s *BackupService) { s.replicator = r }
}

type job struct {
	backupID int64
	cancel   context.CancelFunc
}

func NewBackupService(
	containers domain.ContainerRuntime,
	engine domain.SnapshotEngine,
	storages domain.StorageRepository,
	history domain.BackupHistoryRepository,
	publisher domain.EventPublisher,
	logger *slog.Logger,
	opts ...BackupOption,
) *BackupService {
	if logger == nil {
		logger = slog.Default()
	}
	s := &BackupService{
		containers:    containers,
		engine:        engine,
		storages:      storages,
		history:       history,
		publisher:     publisher,
		logger:        logger,
		strictPartial: true, // défaut sûr : partiel = échec
		running:       map[string]*job{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start : valide, enregistre un run pending, lance le pipeline en async.
// ErrConflict si un run est déjà en cours sur ce conteneur.
func (s *BackupService) Start(ctx context.Context, req domain.BackupRequest) (*domain.BackupRecord, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	storage, err := s.storages.GetByID(ctx, req.StorageID)
	if err != nil {
		return nil, fmt.Errorf("loading storage config: %w", err)
	}
	if err := storage.EnsureBackupTarget(); err != nil {
		return nil, err
	}
	target, err := s.containers.Get(ctx, req.ContainerID)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}
	mounts, err := selectMounts(target, req.Volumes)
	if err != nil {
		return nil, err
	}

	rec := &domain.BackupRecord{
		ContainerID:   target.ID,
		ContainerName: target.Name,
		StorageID:     storage.ID,
		Status:        domain.BackupPending,
		TriggeredBy:   req.TriggeredBy,
		StartTime:     time.Now().UTC(),
	}

	s.mu.Lock()
	if _, busy := s.running[target.ID]; busy {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: a backup of container %s is already running", domain.ErrConflict, target.Name)
	}
	if err := s.history.Create(ctx, rec); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("recording backup run: %w", err)
	}
	// contexte détaché de la requête HTTP mais annulable via Cancel/Close
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.running[target.ID] = &job{backupID: rec.ID, cancel: cancel}
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			delete(s.running, target.ID)
			s.mu.Unlock()
		}()
		s.execute(jobCtx, rec, target, storage, req, mounts)
	}()
	return rec, nil
}

func (s *BackupService) Cancel(backupID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.running {
		if j.backupID == backupID {
			j.cancel()
			return nil
		}
	}
	return fmt.Errorf("%w: no running backup with id %d", domain.ErrNotFound, backupID)
}

// Close : annule tous les jobs et attend leurs rollbacks, borné par ctx.
func (s *BackupService) Close(ctx context.Context) error {
	s.mu.Lock()
	for _, j := range s.running {
		j.cancel()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *BackupService) History(ctx context.Context, filter domain.HistoryFilter) ([]domain.BackupRecord, error) {
	return s.history.List(ctx, filter)
}

func (s *BackupService) GetRecord(ctx context.Context, id int64) (*domain.BackupRecord, error) {
	return s.history.GetByID(ctx, id)
}

func (s *BackupService) execute(ctx context.Context, rec *domain.BackupRecord, target *domain.Container,
	storage *domain.StorageConfig, req domain.BackupRequest, mounts []domain.Mount) {

	log := s.logger.With("backup_id", rec.ID, "container", target.Name, "storage", storage.Name,
		"actor", rec.TriggeredBy.String())
	log.Info("backup started")
	s.transition(ctx, rec, domain.BackupRunning, "backup started")

	var warnings []string

	// 1. pre-hook — défaut abort : sauvegarder de l'incohérent est pire
	// que ne pas sauvegarder
	if req.PreHook != nil {
		warn, err := s.runHook(ctx, target.ID, req.PreHook, domain.HookAbort, "pre-hook", rec.ID)
		if err != nil {
			s.finish(ctx, rec, err, warnings, log)
			return
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// 2. arrêt pour sauvegarde à froid
	stoppedByUs := false
	if req.StopContainer && target.IsRunning() {
		if err := s.containers.Stop(ctx, target.ID, 0); err != nil {
			s.finish(ctx, rec, fmt.Errorf("stopping container: %w", err), warnings, log)
			return
		}
		stoppedByUs = true
		s.event(rec.ID, domain.EventLog, "container stopped for cold backup")
	}

	// 3. snapshot via le worker éphémère
	summary, backupErr := s.snapshot(ctx, rec, storage, target, mounts, req.Tags)
	if errors.Is(backupErr, domain.ErrPartial) {
		// En mode strict l'erreur est conservée : le run finit en `failed`
		// et le snapshot incomplet n'est jamais présenté comme utilisable.
		if s.strictPartial {
			log.Warn("partial backup rejected (strict mode)", "detail", backupErr.Error())
		} else {
			warnings = append(warnings, backupErr.Error())
			backupErr = nil
		}
	}
	if summary != nil {
		rec.SnapshotID = summary.SnapshotID
		rec.BytesProcessed = summary.BytesProcessed
	}

	// 4. rollback : quoi qu'il soit arrivé, le conteneur repart — sur un
	// contexte qui survit à l'annulation
	var restartErr error
	if stoppedByUs {
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		restartErr = s.containers.Start(restartCtx, target.ID)
		cancel()
		if restartErr != nil {
			restartErr = fmt.Errorf("CRITICAL: container %s could not be restarted after backup: %w", target.Name, restartErr)
			log.Error("container restart failed", "error", restartErr)
			s.event(rec.ID, domain.EventError, restartErr.Error())
			backupErr = errors.Join(backupErr, restartErr)
		} else {
			s.event(rec.ID, domain.EventLog, "container restarted")
		}
	}

	// 5. post-hook — défaut continue : un nettoyage raté n'invalide pas un
	// bon snapshot ; tourne même après échec tant que le conteneur est up
	if req.PostHook != nil && restartErr == nil {
		warn, err := s.runHook(ctx, target.ID, req.PostHook, domain.HookContinue, "post-hook", rec.ID)
		if err != nil {
			backupErr = errors.Join(backupErr, err)
		} else if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// 6. copie secondaire (3-2-1), AVANT la rétention : appliquer la politique
	// du dépôt principal d'abord ferait courir le risque d'effacer un snapshot
	// qui n'existe encore qu'à un seul exemplaire.
	//
	// Son échec est un AVERTISSEMENT : le snapshot existe et est restaurable
	// depuis le dépôt principal — annoncer `failed` ferait croire qu'il n'y a
	// pas de sauvegarde du tout, ce qui est plus dangereux que le contraire.
	// Le trou reste visible et rattrapé : alerte sur avertissement, passe de
	// réconciliation, et sdb_replication_pending_snapshots non nul tant que la
	// copie manque.
	if backupErr == nil && s.replicator != nil && rec.SnapshotID != "" {
		if err := s.replicator.ReplicateAfterBackup(ctx, storage.ID, rec.ID, rec.SnapshotID); err != nil {
			msg := "secondary copy failed: " + err.Error()
			warnings = append(warnings, msg)
			s.event(rec.ID, domain.EventError, msg)
			log.Error("secondary copy failed", "error", err)
		}
	}

	// 7. rétention après succès seulement ; son échec = warning, pas échec.
	// Un dépôt append-only ne subit jamais forget/prune : la politique est
	// ignorée bruyamment plutôt que d'effacer des snapshots protégés.
	if backupErr == nil && req.Retention != nil {
		if err := storage.EnsureMutable("retention"); err != nil {
			msg := "retention skipped: " + err.Error()
			warnings = append(warnings, msg)
			s.event(rec.ID, domain.EventLog, msg)
			log.Warn("retention skipped on append-only storage", "storage", storage.Name)
		} else if err := s.engine.Forget(ctx, storage, *req.Retention); err != nil {
			msg := "retention failed: " + err.Error()
			warnings = append(warnings, msg)
			s.event(rec.ID, domain.EventError, msg)
		}
	}

	s.finish(ctx, rec, backupErr, warnings, log)
}

// snapshot : EnsureRepository + Backup, événements relayés au publisher.
// Fermer events après le retour est sûr : RunWorker garantit qu'aucune
// écriture ne survient ensuite.
func (s *BackupService) snapshot(ctx context.Context, rec *domain.BackupRecord, storage *domain.StorageConfig,
	target *domain.Container, mounts []domain.Mount, extraTags []string) (*domain.BackupSummary, error) {

	if err := s.engine.EnsureRepository(ctx, storage); err != nil {
		return nil, fmt.Errorf("preparing repository: %w", err)
	}
	tags := append([]string{"container:" + target.Name}, extraTags...)

	events := make(chan domain.ProgressEvent, 64)
	var fwd sync.WaitGroup
	fwd.Add(1)
	go func() {
		defer fwd.Done()
		for ev := range events {
			s.publisher.Publish(ev)
		}
	}()
	summary, err := s.engine.Backup(ctx, storage, rec.ID, mounts, tags, events)
	close(events)
	fwd.Wait()
	return summary, err
}

// runHook : erreur fatale (politique abort) ou message d'avertissement.
func (s *BackupService) runHook(ctx context.Context, containerID string, hook *domain.Hook,
	def domain.HookFailurePolicy, name string, backupID int64) (warning string, fatal error) {

	s.event(backupID, domain.EventLog, name+": "+strings.Join(hook.Command, " "))
	res, err := s.containers.Exec(ctx, containerID, hook.Command, hook.EffectiveTimeout())

	var failure string
	switch {
	case err != nil:
		failure = fmt.Sprintf("%s failed: %v", name, err)
	case res.ExitCode != 0:
		failure = fmt.Sprintf("%s failed (exit %d): %s", name, res.ExitCode, strings.TrimSpace(res.Stderr))
	default:
		return "", nil
	}
	if hook.EffectiveOnFailure(def) == domain.HookAbort {
		return "", errors.New(failure)
	}
	s.event(backupID, domain.EventError, failure)
	return failure, nil
}

func (s *BackupService) transition(ctx context.Context, rec *domain.BackupRecord, status domain.BackupStatus, msg string) {
	rec.Status = status
	if err := s.history.Update(ctx, rec); err != nil {
		s.logger.Error("persisting backup status", "backup_id", rec.ID, "error", err)
	}
	s.publisher.Publish(domain.ProgressEvent{
		BackupID:  rec.ID,
		Container: rec.ContainerName,
		Type:      domain.EventStatus,
		Time:      time.Now().UTC(),
		Status:    status,
		Message:   msg,
	})
}

func (s *BackupService) event(backupID int64, typ domain.EventType, msg string) {
	s.publisher.Publish(domain.ProgressEvent{
		BackupID: backupID,
		Type:     typ,
		Time:     time.Now().UTC(),
		Message:  msg,
	})
}

// finish : classe le résultat, persiste l'état terminal (contexte
// insensible à l'annulation) et publie le statut final.
func (s *BackupService) finish(ctx context.Context, rec *domain.BackupRecord, err error, warnings []string, log *slog.Logger) {
	now := time.Now().UTC()
	rec.EndTime = &now

	switch {
	case err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrCanceled)):
		rec.Status = domain.BackupCanceled
	case err != nil:
		rec.Status = domain.BackupFailed
	case len(warnings) > 0:
		rec.Status = domain.BackupWarning
	default:
		rec.Status = domain.BackupSuccess
	}

	var parts []string
	if err != nil {
		parts = append(parts, err.Error())
	}
	parts = append(parts, warnings...)
	rec.ErrorLog = strings.Join(parts, "\n")

	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if uerr := s.history.Update(saveCtx, rec); uerr != nil {
		s.logger.Error("persisting terminal backup status", "backup_id", rec.ID, "error", uerr)
	}
	s.publisher.Publish(domain.ProgressEvent{
		BackupID:   rec.ID,
		Container:  rec.ContainerName,
		Type:       domain.EventStatus,
		Time:       now,
		Status:     rec.Status,
		Message:    rec.ErrorLog,
		SnapshotID: rec.SnapshotID,
	})

	switch rec.Status {
	case domain.BackupSuccess:
		log.Info("backup finished", "snapshot", rec.SnapshotID, "bytes", rec.BytesProcessed)
	case domain.BackupWarning:
		log.Warn("backup finished with warnings", "snapshot", rec.SnapshotID, "warnings", warnings)
	case domain.BackupCanceled:
		log.Warn("backup canceled")
	default:
		log.Error("backup failed", "error", rec.ErrorLog)
	}
}

// selectMounts : sous-ensemble demandé, sinon tous les montages
// sauvegardables.
func selectMounts(c *domain.Container, volumes []string) ([]domain.Mount, error) {
	backupable := c.BackupableMounts()
	if len(volumes) == 0 {
		if len(backupable) == 0 {
			return nil, fmt.Errorf("%w: container %s has no backupable volumes", domain.ErrInvalidInput, c.Name)
		}
		return backupable, nil
	}
	byName := map[string]domain.Mount{}
	for _, m := range backupable {
		if m.Name != "" {
			byName[m.Name] = m
		}
	}
	out := make([]domain.Mount, 0, len(volumes))
	for _, v := range volumes {
		m, ok := byName[v]
		if !ok {
			return nil, fmt.Errorf("%w: volume %q is not mounted by container %s", domain.ErrInvalidInput, v, c.Name)
		}
		out = append(out, m)
	}
	return out, nil
}
