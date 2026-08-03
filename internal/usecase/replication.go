package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// ReplicationService : la seconde copie de la règle 3-2-1.
//
// Un dépôt unique par sauvegarde, c'est un seul support : le perdre ou le
// corrompre perd tout. Le verrou append-only protège de la SUPPRESSION, pas de
// la perte du support — seule une copie sur un second support le fait.
//
// Deux déclencheurs pour une seule mécanique :
//   - après chaque sauvegarde réussie, le snapshot est copié tout de suite
//     (le délai avant d'être à deux exemplaires se compte en minutes) ;
//   - une passe de réconciliation périodique recopie ce qui manque, ce qui
//     rattrape les copies échouées et les sauvegardes faites pendant une panne
//     de la destination.
//
// L'état de référence n'est pas tenu par SDB mais LU dans les deux dépôts :
// une colonne « copié » pourrait mentir après une restauration de base ou une
// suppression manuelle, la comparaison des snapshots, non.
type ReplicationService struct {
	engine    domain.SnapshotEngine
	storages  domain.StorageRepository
	publisher domain.EventPublisher
	logger    *slog.Logger
	observer  func(ReplicationStatus)

	mu    sync.Mutex
	locks map[int64]*sync.Mutex // un verrou par dépôt de copie
}

// ReplicationOption : réglage optionnel du service, appliqué à la construction.
type ReplicationOption func(*ReplicationService)

// WithReplicationObserver : appelé après chaque évaluation d'une paire.
// Sert au collecteur Prometheus, que le usecase n'a pas à connaître.
func WithReplicationObserver(fn func(ReplicationStatus)) ReplicationOption {
	return func(s *ReplicationService) { s.observer = fn }
}

func NewReplicationService(
	engine domain.SnapshotEngine,
	storages domain.StorageRepository,
	publisher domain.EventPublisher,
	logger *slog.Logger,
	opts ...ReplicationOption,
) *ReplicationService {
	if logger == nil {
		logger = slog.Default()
	}
	s := &ReplicationService{
		engine: engine, storages: storages, publisher: publisher, logger: logger,
		locks: map[int64]*sync.Mutex{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ReplicationStatus : écart entre un dépôt et sa copie secondaire, à l'instant
// de la mesure.
type ReplicationStatus struct {
	CopyID     int64
	CopyName   string
	SourceID   int64
	SourceName string
	// SourceSnapshots / CopiedSnapshots : snapshots présents de chaque côté.
	SourceSnapshots int
	CopiedSnapshots int
	// Pending : snapshots de la source absents de la copie.
	Pending int
	// OldestPending : date du plus ancien snapshot non copié. C'est de LUI que
	// se déduit l'ancienneté du retard : le plus récent non copié donnerait
	// toujours un retard proche de zéro, même avec des semaines de trou.
	OldestPending *time.Time
	CheckedAt     time.Time
}

// Lag : ancienneté du retard de réplication. Zéro quand tout est copié.
func (s ReplicationStatus) Lag() time.Duration {
	if s.OldestPending == nil {
		return 0
	}
	return s.CheckedAt.Sub(*s.OldestPending)
}

// pair : un dépôt de copie et sa source, résolus ensemble.
type pair struct {
	copy   *domain.StorageConfig
	source *domain.StorageConfig
}

// pairs : toutes les paires source → copie configurées.
func (s *ReplicationService) pairs(ctx context.Context) ([]pair, error) {
	configs, err := s.storages.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing storage configs: %w", err)
	}
	byID := map[int64]*domain.StorageConfig{}
	for i := range configs {
		byID[configs[i].ID] = &configs[i]
	}
	var out []pair
	for i := range configs {
		cfg := &configs[i]
		if !cfg.IsCopyTarget() {
			continue
		}
		src, ok := byID[cfg.CopyOf]
		if !ok {
			// la clé étrangère l'interdit ; si ça arrive, le dire fort plutôt
			// que sauter une copie en silence
			s.logger.Error("secondary copy points at a missing storage",
				"storage", cfg.Name, "copy_of", cfg.CopyOf)
			continue
		}
		out = append(out, pair{copy: cfg, source: src})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].copy.ID < out[j].copy.ID })
	return out, nil
}

// Configured : nombre de paires source → copie déclarées. Lecture en base
// seule, les dépôts ne sont pas interrogés — sert au démarrage, où signaler
// l'absence de seconde copie ne doit coûter aucun appel réseau.
func (s *ReplicationService) Configured(ctx context.Context) (int, error) {
	pairs, err := s.pairs(ctx)
	return len(pairs), err
}

// CopiesOf : dépôts de copie alimentés par la source donnée.
func (s *ReplicationService) CopiesOf(ctx context.Context, sourceID int64) ([]domain.StorageConfig, error) {
	pairs, err := s.pairs(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.StorageConfig
	for _, p := range pairs {
		if p.source.ID == sourceID {
			out = append(out, *p.copy)
		}
	}
	return out, nil
}

// Status : écart mesuré pour une copie, sans rien répliquer.
func (s *ReplicationService) Status(ctx context.Context, copyID int64) (*ReplicationStatus, error) {
	p, err := s.pairFor(ctx, copyID)
	if err != nil {
		return nil, err
	}
	return s.measure(ctx, p)
}

// StatusAll : écart de chaque paire. Une copie injoignable n'empêche pas de
// mesurer les autres.
func (s *ReplicationService) StatusAll(ctx context.Context) ([]ReplicationStatus, error) {
	pairs, err := s.pairs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ReplicationStatus, 0, len(pairs))
	var failures []error
	for _, p := range pairs {
		st, err := s.measure(ctx, p)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", p.copy.Name, err))
			continue
		}
		out = append(out, *st)
	}
	return out, errors.Join(failures...)
}

// Replicate : copie TOUT ce qui manque dans une copie secondaire, puis
// remesure. Bloquant : appelé par la passe planifiée ou un job HTTP détaché,
// jamais dans le fil d'une requête.
func (s *ReplicationService) Replicate(ctx context.Context, copyID int64) (*ReplicationStatus, error) {
	p, err := s.pairFor(ctx, copyID)
	if err != nil {
		return nil, err
	}
	unlock := s.lock(copyID)
	defer unlock()

	log := s.logger.With("storage", p.source.Name, "copy", p.copy.Name)
	if err := s.engine.EnsureCopyTarget(ctx, p.copy, p.source); err != nil {
		return nil, fmt.Errorf("preparing copy target: %w", err)
	}
	start := time.Now()
	if err := s.copySnapshots(ctx, p, nil, 0); err != nil {
		return nil, err
	}
	st, err := s.measure(ctx, p)
	if err != nil {
		return nil, err
	}
	log.Info("replication pass finished",
		"duration", time.Since(start).Round(time.Second).String(),
		"copied_snapshots", st.CopiedSnapshots, "pending", st.Pending)
	return st, nil
}

// ReplicateAll : une passe sur toutes les paires. Une copie en échec
// n'interrompt pas les suivantes.
func (s *ReplicationService) ReplicateAll(ctx context.Context) error {
	pairs, err := s.pairs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, p := range pairs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.Replicate(ctx, p.copy.ID); err != nil {
			s.logger.Error("replication failed", "copy", p.copy.Name, "error", err)
			failures = append(failures, fmt.Errorf("%s: %w", p.copy.Name, err))
		}
	}
	return errors.Join(failures...)
}

// ReplicateAfterBackup : copie le snapshot qui vient d'être créé, vers chaque
// copie secondaire de son dépôt. Retourne une erreur agrégée ; l'appelant en
// fait un AVERTISSEMENT, pas un échec — la sauvegarde, elle, existe bel et
// bien sur le dépôt principal, et la passe de réconciliation retentera.
func (s *ReplicationService) ReplicateAfterBackup(ctx context.Context, sourceID, backupID int64, snapshotID string) error {
	if snapshotID == "" {
		return nil
	}
	copies, err := s.CopiesOf(ctx, sourceID)
	if err != nil {
		return err
	}
	if len(copies) == 0 {
		return nil
	}
	src, err := s.storages.GetByID(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("loading copy source: %w", err)
	}

	var failures []error
	for i := range copies {
		cfg := &copies[i]
		p := pair{copy: cfg, source: src}

		unlock := s.lock(cfg.ID)
		err = s.engine.EnsureCopyTarget(ctx, cfg, src)
		if err == nil {
			err = s.copySnapshots(ctx, p, []string{snapshotID}, backupID)
		}
		unlock()

		if err != nil {
			failures = append(failures, fmt.Errorf("copy to %s: %w", cfg.Name, err))
			continue
		}
		s.logger.Info("snapshot replicated", "copy", cfg.Name, "snapshot", snapshotID, "backup_id", backupID)
	}
	return errors.Join(failures...)
}

// Schedule : passe de réconciliation périodique. Premier passage après un
// intervalle complet — au démarrage, la copie inline vient de faire le travail.
func (s *ReplicationService) Schedule(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	s.logger.Info("secondary copy reconciliation enabled", "interval", every.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.ReplicateAll(ctx); err != nil {
				s.logger.Error("replication pass finished with failures", "error", err)
			}
		}
	}
}

// copySnapshots : exécute la copie, événements relayés au publisher. Les
// événements portent l'ID de la sauvegarde quand la copie suit un run, pour
// que l'UI les rattache à la bonne ligne d'historique.
func (s *ReplicationService) copySnapshots(ctx context.Context, p pair, ids []string, backupID int64) error {
	events := make(chan domain.ProgressEvent, 64)
	var fwd sync.WaitGroup
	fwd.Add(1)
	go func() {
		defer fwd.Done()
		for ev := range events {
			ev.BackupID = backupID
			s.publisher.Publish(ev)
		}
	}()
	err := s.engine.Copy(ctx, p.copy, p.source, ids, events)
	close(events)
	fwd.Wait()
	return err
}

// measure : compare les snapshots des deux dépôts.
//
// La copie ré-encrypte, donc les identifiants DIFFÈRENT d'un dépôt à l'autre :
// comparer par ID compterait tout comme manquant. restic préserve en revanche
// la date et les chemins archivés, qui identifient un snapshot de façon stable.
func (s *ReplicationService) measure(ctx context.Context, p pair) (*ReplicationStatus, error) {
	sourceSnaps, err := s.engine.Snapshots(ctx, p.source, nil)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots of %s: %w", p.source.Name, err)
	}
	copySnaps, err := s.engine.Snapshots(ctx, p.copy, nil)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots of %s: %w", p.copy.Name, err)
	}

	copied := make(map[string]struct{}, len(copySnaps))
	for _, snap := range copySnaps {
		copied[snapshotKey(snap)] = struct{}{}
	}
	st := &ReplicationStatus{
		CopyID: p.copy.ID, CopyName: p.copy.Name,
		SourceID: p.source.ID, SourceName: p.source.Name,
		SourceSnapshots: len(sourceSnaps), CopiedSnapshots: len(copySnaps),
		CheckedAt: time.Now().UTC(),
	}
	for _, snap := range sourceSnaps {
		if _, ok := copied[snapshotKey(snap)]; ok {
			continue
		}
		st.Pending++
		if st.OldestPending == nil || snap.Time.Before(*st.OldestPending) {
			t := snap.Time
			st.OldestPending = &t
		}
	}
	if s.observer != nil {
		s.observer(*st)
	}
	return st, nil
}

// snapshotKey : identité d'un snapshot indépendante du dépôt qui l'héberge.
func snapshotKey(s domain.Snapshot) string {
	paths := append([]string(nil), s.Paths...)
	sort.Strings(paths)
	return s.Time.UTC().Format(time.RFC3339Nano) + "|" + strings.Join(paths, ",")
}

func (s *ReplicationService) pairFor(ctx context.Context, copyID int64) (pair, error) {
	cfg, err := s.storages.GetByID(ctx, copyID)
	if err != nil {
		return pair{}, err
	}
	if !cfg.IsCopyTarget() {
		return pair{}, fmt.Errorf("%w: storage %q is not a secondary copy", domain.ErrInvalidInput, cfg.Name)
	}
	src, err := s.storages.GetByID(ctx, cfg.CopyOf)
	if err != nil {
		return pair{}, fmt.Errorf("loading copy source: %w", err)
	}
	return pair{copy: cfg, source: src}, nil
}

// lock : sérialise les copies visant un même dépôt. Deux `restic copy`
// concurrents sur la même destination se disputeraient son verrou et l'un des
// deux échouerait — ici la copie inline et la passe de réconciliation
// s'attendent au lieu de se marcher dessus.
func (s *ReplicationService) lock(copyID int64) func() {
	s.mu.Lock()
	mu, ok := s.locks[copyID]
	if !ok {
		mu = &sync.Mutex{}
		s.locks[copyID] = mu
	}
	s.mu.Unlock()

	mu.Lock()
	return mu.Unlock
}
