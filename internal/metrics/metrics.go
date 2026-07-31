// Package metrics : exposition Prometheus. Le collecteur implémente
// EventPublisher — même flux d'événements que le hub WebSocket, les
// usecases ignorent Prometheus.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

type Collector struct {
	registry *prometheus.Registry

	backupsTotal  *prometheus.CounterVec
	restoresTotal *prometheus.CounterVec
	bytesTotal    prometheus.Counter
	runningJobs   prometheus.Gauge
	lastSuccess   *prometheus.GaugeVec
	missedRuns    *prometheus.CounterVec
}

var _ domain.EventPublisher = (*Collector)(nil)

func New(version string) *Collector {
	registry := prometheus.NewRegistry()
	c := &Collector{
		registry: registry,
		backupsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sdb_backups_total",
			Help: "Backup runs by terminal status.",
		}, []string{"status"}),
		restoresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sdb_restores_total",
			Help: "Restore runs by terminal status.",
		}, []string{"status"}),
		bytesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sdb_backup_bytes_processed_total",
			Help: "Bytes processed by successful and partial backups.",
		}),
		runningJobs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sdb_running_jobs",
			Help: "Backups and restores currently in flight.",
		}),
		// alerte type : conteneur sans sauvegarde récente
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "sdb_last_backup_success_timestamp_seconds",
			Help: "Unix time of the last successful backup, per container (alert when stale).",
		}, []string{"container"}),
		// alerte type : SDB est reste arrete au-dela d'une fenetre planifiee.
		// Sans ce compteur, le trou est invisible.
		missedRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sdb_schedule_missed_runs_total",
			Help: "Scheduled windows that elapsed while SDB was down, per schedule (alert on any increase).",
		}, []string{"schedule", "container"}),
	}

	info := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "sdb_build_info",
		Help:        "Build information.",
		ConstLabels: prometheus.Labels{"version": version},
	})
	info.Set(1)

	registry.MustRegister(
		c.backupsTotal, c.restoresTotal, c.bytesTotal, c.runningJobs, c.lastSuccess, c.missedRuns, info,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return c
}

// Publish : ne bloque jamais (compteurs en mémoire).
func (c *Collector) Publish(ev domain.ProgressEvent) {
	if ev.Type == domain.EventSummary && ev.BackupID != 0 {
		if ev.BytesDone > 0 {
			c.bytesTotal.Add(float64(ev.BytesDone))
		}
		return
	}
	if ev.Type != domain.EventStatus {
		return
	}
	switch ev.Status {
	case domain.BackupRunning:
		c.runningJobs.Inc()
		return
	case domain.BackupSuccess, domain.BackupWarning, domain.BackupFailed, domain.BackupCanceled:
	default:
		return
	}

	// transition terminale
	c.runningJobs.Dec()
	status := string(ev.Status)
	if ev.RestoreID != 0 {
		c.restoresTotal.WithLabelValues(status).Inc()
		return
	}
	c.backupsTotal.WithLabelValues(status).Inc()
	if ev.Status == domain.BackupSuccess || ev.Status == domain.BackupWarning {
		if ev.Container != "" {
			c.lastSuccess.WithLabelValues(ev.Container).SetToCurrentTime()
		}
	}
}

func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}

// RecordMissedRuns : echeances tombees pendant un arret de SDB.
func (c *Collector) RecordMissedRuns(schedule, container string, missed int) {
	if missed <= 0 {
		return
	}
	c.missedRuns.WithLabelValues(schedule, container).Add(float64(missed))
}
