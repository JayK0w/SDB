package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// SchedulerService : CRUD des planifications + moteur cron qui les tire.
// robfig/cron est une lib de timing pure (comparable à time.Ticker), d'où
// sa présence tolérée dans la couche usecase. Expressions 5 champs, heure
// serveur (UTC dans le conteneur livré).
type SchedulerService struct {
	schedules domain.ScheduleRepository
	backups   *BackupService
	logger    *slog.Logger

	// catchUp : rejouer UNE fois les planifications dont l'échéance est
	// passée pendant un arrêt de SDB.
	catchUp bool
	// onMissed : notifié pour chaque planification en retard, même sans
	// rattrapage. C'est le canal qui rend le trou visible.
	onMissed func(sched domain.BackupSchedule, missed int)

	mu      sync.Mutex
	cron    *cron.Cron
	entries map[int64]cron.EntryID
}

// SchedulerOption : réglage optionnel, appliqué à la construction.
type SchedulerOption func(*SchedulerService)

// WithCatchUp : rejoue au démarrage les planifications dont l'échéance est
// passée pendant l'arrêt.
//
// Désactivé par défaut, et c'est délibéré : une sauvegarde peut ARRÊTER son
// conteneur. Rejouer automatiquement dix planifications au redémarrage d'un
// hôte qui vient de tomber, c'est stopper dix services en production au pire
// moment. Le trou reste signalé dans tous les cas ; seul le rattrapage
// automatique est un choix.
func WithCatchUp(enabled bool) SchedulerOption {
	return func(s *SchedulerService) { s.catchUp = enabled }
}

// WithMissedRunHandler : branche l'observabilité (métrique, alerte) sur la
// détection des échéances manquées.
func WithMissedRunHandler(fn func(sched domain.BackupSchedule, missed int)) SchedulerOption {
	return func(s *SchedulerService) { s.onMissed = fn }
}

func NewSchedulerService(schedules domain.ScheduleRepository, backups *BackupService, logger *slog.Logger, opts ...SchedulerOption) *SchedulerService {
	if logger == nil {
		logger = slog.Default()
	}
	s := &SchedulerService{
		schedules: schedules,
		backups:   backups,
		logger:    logger,
		cron:      cron.New(),
		entries:   map[int64]cron.EntryID{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// maxCountedMisses : plafond du comptage. Après un arrêt long, savoir qu'il
// manque « au moins 50 » runs suffit ; itérer sur des années d'échéances ne
// renseigne pas davantage et coûte du temps de démarrage.
const maxCountedMisses = 50

// missedRuns : nombre d'échéances tombées entre le dernier run et
// maintenant. 0 si la planification est à jour ou n'a jamais tourné (il n'y
// a alors pas de trou, juste une planification neuve).
func missedRuns(spec string, lastRun *time.Time, now time.Time) (int, error) {
	if lastRun == nil {
		return 0, nil
	}
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid cron expression %q: %v", domain.ErrInvalidInput, spec, err)
	}
	count := 0
	// strictement AVANT maintenant : une échéance qui tombe pile à l'instant
	// présent est sur le point d'être tirée par le cron vivant, la compter
	// comme manquée produirait une fausse alerte.
	for t := schedule.Next(*lastRun); t.Before(now); t = schedule.Next(t) {
		count++
		if count >= maxCountedMisses {
			break
		}
	}
	return count, nil
}

func ValidateCron(spec string) error {
	if _, err := cron.ParseStandard(spec); err != nil {
		return fmt.Errorf("%w: invalid cron expression %q: %v", domain.ErrInvalidInput, spec, err)
	}
	return nil
}

// Run : charge les planifications, démarre le cron, bloque jusqu'à
// annulation. Les backups tirés appartiennent au BackupService.
func (s *SchedulerService) Run(ctx context.Context) error {
	if err := s.reload(ctx); err != nil {
		return err
	}
	s.cron.Start()
	s.logger.Info("backup scheduler started")
	<-ctx.Done()
	<-s.cron.Stop().Done()
	return nil
}

// reload : reconstruit toutes les entrées cron (peu de planifs → rebuild
// complet plus simple et sûr que la chirurgie d'entrées).
func (s *SchedulerService) reload(ctx context.Context) error {
	all, err := s.schedules.List(ctx)
	if err != nil {
		return fmt.Errorf("loading schedules: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entryID := range s.entries {
		s.cron.Remove(entryID)
	}
	s.entries = map[int64]cron.EntryID{}

	for _, sched := range all {
		if !sched.Enabled {
			continue
		}
		sched := sched // capture par itération
		entryID, err := s.cron.AddFunc(sched.Cron, func() { s.fire(sched) })
		if err != nil {
			// expression corrompue : on saute plutôt que bloquer les autres
			s.logger.Error("skipping schedule with invalid cron expression",
				"schedule", sched.Name, "cron", sched.Cron, "error", err)
			continue
		}
		s.entries[sched.ID] = entryID
	}
	s.logger.Info("schedules loaded", "active", len(s.entries), "total", len(all))

	// hors verrou : le rattrapage démarre des sauvegardes, qui reviennent
	// vers le service
	go s.reportMissed(all)
	return nil
}

// reportMissed : signale les échéances tombées pendant un arrêt de SDB, et
// rejoue au plus UNE fois par planification si le rattrapage est activé.
//
// Sans ça, une coupure de trois heures saute silencieusement les sauvegardes
// concernées : l'exploitant continue de croire à sa couverture jusqu'au jour
// où il en cherche une.
func (s *SchedulerService) reportMissed(all []domain.BackupSchedule) {
	now := time.Now()
	for _, sched := range all {
		if !sched.Enabled {
			continue
		}
		missed, err := missedRuns(sched.Cron, sched.LastRunAt, now)
		if err != nil || missed == 0 {
			continue
		}

		s.logger.Warn("schedule missed its window while SDB was down",
			"schedule", sched.Name, "container", sched.ContainerName,
			"missed_runs", missed, "last_run", sched.LastRunAt.UTC().Format(time.RFC3339),
			"catch_up", s.catchUp)
		if s.onMissed != nil {
			s.onMissed(sched, missed)
		}

		if !s.catchUp {
			continue
		}
		// une seule reprise, quel que soit le nombre d'échéances ratées :
		// dérouler l'arriéré n'apporte rien, les snapshots seraient
		// identiques et se dédupliqueraient
		req := sched.ToRequest()
		req.TriggeredBy = domain.SystemActor("catchup:" + sched.Name)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rec, err := s.backups.Start(ctx, req)
		cancel()
		if err != nil {
			s.logger.Error("catch-up run could not start", "schedule", sched.Name, "error", err)
			continue
		}
		s.logger.Info("catch-up run started", "schedule", sched.Name, "backup_id", rec.ID)
		touchCtx, touchCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := s.schedules.TouchLastRun(touchCtx, sched.ID, time.Now().UTC()); err != nil {
			s.logger.Error("recording catch-up run", "schedule", sched.Name, "error", err)
		}
		touchCancel()
	}
}

// fire : un conflit (run précédent encore en cours) est loggé et sauté —
// pas de sauvegardes superposées sur un même conteneur.
func (s *SchedulerService) fire(sched domain.BackupSchedule) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := s.logger.With("schedule", sched.Name, "container", sched.ContainerName)
	rec, err := s.backups.Start(ctx, sched.ToRequest())
	if err != nil {
		log.Error("scheduled backup could not start", "error", err)
		return
	}
	log.Info("scheduled backup fired", "backup_id", rec.ID)
	if err := s.schedules.TouchLastRun(ctx, sched.ID, time.Now().UTC()); err != nil {
		log.Error("recording schedule run", "error", err)
	}
}

// RunNow : déclenchement manuel hors cadence.
// RunNow : déclenchement manuel d'une planification. L'acteur est l'humain
// qui a cliqué, pas le planificateur — ToRequest() attribue au système, on
// écrase donc l'attribution ici.
func (s *SchedulerService) RunNow(ctx context.Context, id int64, actor domain.Actor) (*domain.BackupRecord, error) {
	sched, err := s.schedules.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	req := sched.ToRequest()
	req.TriggeredBy = actor
	rec, err := s.backups.Start(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.schedules.TouchLastRun(ctx, id, time.Now().UTC()); err != nil {
		s.logger.Error("recording schedule run", "schedule", sched.Name, "error", err)
	}
	return rec, nil
}

// CRUD : valide, persiste, recharge à chaud les entrées cron.

func (s *SchedulerService) Create(ctx context.Context, sched *domain.BackupSchedule) error {
	if err := sched.Validate(); err != nil {
		return err
	}
	if err := ValidateCron(sched.Cron); err != nil {
		return err
	}
	if err := s.schedules.Create(ctx, sched); err != nil {
		return err
	}
	return s.reload(ctx)
}

func (s *SchedulerService) Update(ctx context.Context, sched *domain.BackupSchedule) error {
	if err := sched.Validate(); err != nil {
		return err
	}
	if err := ValidateCron(sched.Cron); err != nil {
		return err
	}
	if err := s.schedules.Update(ctx, sched); err != nil {
		return err
	}
	return s.reload(ctx)
}

func (s *SchedulerService) Delete(ctx context.Context, id int64) error {
	if err := s.schedules.Delete(ctx, id); err != nil {
		return err
	}
	return s.reload(ctx)
}

func (s *SchedulerService) Get(ctx context.Context, id int64) (*domain.BackupSchedule, error) {
	return s.schedules.GetByID(ctx, id)
}

func (s *SchedulerService) List(ctx context.Context) ([]domain.BackupSchedule, error) {
	return s.schedules.List(ctx)
}
