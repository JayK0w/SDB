package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// Noms des tâches périodiques. Ils servent de clé en base : les changer perd
// l'échéance en cours (la tâche repassera une fois, puis reprendra son rythme).
const (
	TaskIntegrityCheck = "integrity-check"
	TaskVerification   = "verification"
	TaskReplication    = "replication"
)

// defaultStartupGrace : délai avant une passe DUE au démarrage. Assez court
// pour que la garantie s'arme le jour même, assez long pour ne pas relire un
// dépôt pendant que l'hôte finit de redémarrer ses services.
const defaultStartupGrace = 5 * time.Minute

// MaintenanceScheduler : arme les boucles périodiques en tenant compte de la
// date du DERNIER passage, pas de la date de démarrage.
//
// Le comportement précédent — attendre un intervalle complet après le boot —
// avait une conséquence qu'aucun log ne signalait : sur une instance
// redémarrée plus souvent que l'intervalle (une mise à jour hebdomadaire avec
// un intervalle de 168 h suffit), la passe ne s'exécutait jamais. La
// vérification de restaurabilité, censée être la preuve que les sauvegardes
// valent quelque chose, était la première concernée.
type MaintenanceScheduler struct {
	state  domain.MaintenanceStateRepository
	logger *slog.Logger
	grace  time.Duration
}

// MaintenanceOption : réglage optionnel, appliqué à la construction.
type MaintenanceOption func(*MaintenanceScheduler)

// WithStartupGrace : délai avant une passe déjà due au démarrage.
func WithStartupGrace(d time.Duration) MaintenanceOption {
	return func(s *MaintenanceScheduler) {
		if d > 0 {
			s.grace = d
		}
	}
}

func NewMaintenanceScheduler(state domain.MaintenanceStateRepository, logger *slog.Logger,
	opts ...MaintenanceOption) *MaintenanceScheduler {

	if logger == nil {
		logger = slog.Default()
	}
	s := &MaintenanceScheduler{state: state, logger: logger, grace: defaultStartupGrace}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Run : exécute fn toutes les `every`, en reprenant l'échéance là où le
// précédent démarrage l'avait laissée. Bloque jusqu'à annulation du contexte.
//
// La date est écrite après CHAQUE passage, réussi ou non : le passage a bien
// eu lieu. Ne pas l'écrire en cas d'échec ferait retenter en boucle une
// vérification qui relit un dépôt entier, ce qui transformerait un incident en
// panne d'exploitation. Le résultat, lui, part par les alertes et les
// métriques.
func (s *MaintenanceScheduler) Run(ctx context.Context, task string, every time.Duration, fn func(context.Context) error) {
	if every <= 0 {
		return
	}
	log := s.logger.With("task", task, "interval", every.String())
	delay := s.firstDelay(ctx, task, every, log)
	log.Info("periodic task armed", "first_pass_in", delay.Round(time.Second).String())

	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		start := time.Now()
		if err := fn(ctx); err != nil {
			log.Error("periodic task finished with failures", "error", err)
		} else {
			log.Info("periodic task finished", "duration", time.Since(start).Round(time.Second).String())
		}
		// contexte détaché : un arrêt pendant la passe ne doit pas empêcher
		// d'enregistrer qu'elle a eu lieu, sinon elle repartirait au boot suivant
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := s.state.MarkRun(markCtx, task, time.Now().UTC()); err != nil {
			log.Error("could not record the run; the next start may repeat it", "error", err)
		}
		cancel()

		timer.Reset(every)
	}
}

// firstDelay : temps restant avant la prochaine échéance.
func (s *MaintenanceScheduler) firstDelay(ctx context.Context, task string, every time.Duration, log *slog.Logger) time.Duration {
	last, err := s.state.LastRun(ctx, task)
	if err != nil {
		// une base illisible ne doit pas provoquer une passe immédiate sur
		// tous les dépôts : on retombe sur l'ancien comportement, bruyamment
		log.Error("could not read the last run date, falling back to a full interval", "error", err)
		return every
	}
	if last.IsZero() {
		log.Info("task has never run on this instance")
		return s.dueDelay(every)
	}
	if remaining := every - time.Since(last); remaining > 0 {
		return remaining
	}
	log.Warn("periodic task is overdue, running shortly after startup",
		"last_run", last.Format(time.RFC3339), "overdue_by", time.Since(last).Round(time.Second).String())
	return s.dueDelay(every)
}

// dueDelay : délai de grâce d'une passe déjà due, borné par l'intervalle
// lui-même. Sans cette borne, un intervalle plus court que la grâce ferait
// attendre PLUS longtemps que le rythme demandé — l'inverse de ce qu'on
// promet à qui règle une vérification toutes les minutes.
func (s *MaintenanceScheduler) dueDelay(every time.Duration) time.Duration {
	if s.grace > every {
		return every
	}
	return s.grace
}
