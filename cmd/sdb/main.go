// Point d'entrée : charge la config, construit chaque couche
// (infra → usecases → API) et gère l'arrêt gracieux.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpapi "github.com/standalone-docker-backup/sdb/internal/api/http"
	"github.com/standalone-docker-backup/sdb/internal/config"
	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/infra/crypto"
	"github.com/standalone-docker-backup/sdb/internal/infra/docker"
	"github.com/standalone-docker-backup/sdb/internal/infra/notify"
	"github.com/standalone-docker-backup/sdb/internal/infra/restic"
	"github.com/standalone-docker-backup/sdb/internal/infra/sqlite"
	"github.com/standalone-docker-backup/sdb/internal/metrics"
	"github.com/standalone-docker-backup/sdb/internal/usecase"
	"github.com/standalone-docker-backup/sdb/web"
)

// injecté au build (-ldflags)
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sdb: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	logger := newLogger(cfg.Log)
	slog.SetDefault(logger)

	// SIGINT/SIGTERM annulent ce contexte → arrêt gracieux
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting SDB", "version", version, "listen", cfg.Server.Addr())
	if !cfg.Server.IsLoopback() {
		logger.Warn("listen address is not loopback-only; if this port is published by Docker, "+
			"bind it on the host side as 127.0.0.1:PORT:PORT — Docker port publishing bypasses UFW/iptables",
			"host", cfg.Server.Host)
	}

	// --- Infra ---
	cipher, err := crypto.NewAESGCM(cfg.Auth.MasterKey)
	if err != nil {
		return fmt.Errorf("initialising secret cipher: %w", err)
	}
	hasher := crypto.NewArgon2idHasher()

	db, err := sqlite.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()
	if err := sqlite.Migrate(ctx, db); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	logger.Info("database ready", "path", cfg.Database.Path)

	// nettoyage des runs interrompus par un crash/redémarrage
	history := sqlite.NewHistoryRepo(db)
	if n, err := history.FailInterrupted(ctx, "interrupted by SDB restart"); err != nil {
		return fmt.Errorf("cleaning interrupted runs: %w", err)
	} else if n > 0 {
		logger.Warn("marked interrupted backup runs as failed", "count", n)
	}
	restoreHistory := sqlite.NewRestoreRepo(db)
	if n, err := restoreHistory.FailInterrupted(ctx, "interrupted by SDB restart"); err != nil {
		return fmt.Errorf("cleaning interrupted restores: %w", err)
	} else if n > 0 {
		logger.Warn("marked interrupted restores as failed", "count", n)
	}

	runtime, err := docker.New(docker.Options{
		Host:        cfg.Docker.Host,
		TLSCACert:   cfg.Docker.TLSCACert,
		TLSCert:     cfg.Docker.TLSCert,
		TLSKey:      cfg.Docker.TLSKey,
		StopTimeout: cfg.Docker.StopTimeout,
	}, logger)
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}
	defer runtime.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := runtime.Ping(pingCtx); err != nil {
		logger.Warn("docker daemon not reachable yet; backups will fail until it is", "error", err)
	} else {
		logger.Info("docker daemon reachable")
	}
	cancel()

	userRepo := sqlite.NewUserRepo(db)
	storageRepo := sqlite.NewStorageRepo(db, cipher)
	engine := restic.New(runtime, cfg.Docker.WorkerImage,
		restic.WithReadDataSubset(cfg.Maintenance.ReadDataSubset))

	// --- Usecases ---
	authSvc := usecase.NewAuthService(userRepo, hasher, logger)
	created, generated, err := authSvc.EnsureInitialAdmin(ctx, cfg.Auth.AdminUsername, cfg.Auth.AdminPassword)
	if err != nil {
		return fmt.Errorf("bootstrapping admin account: %w", err)
	}
	if created {
		if generated != "" {
			logger.Warn("initial admin account created with a generated password — change it after first login",
				"username", cfg.Auth.AdminUsername, "password", generated)
		} else {
			logger.Info("initial admin account created", "username", cfg.Auth.AdminUsername)
		}
	}

	maintenance := usecase.NewMaintenanceService(storageRepo, engine, logger)
	if cfg.Maintenance.CheckInterval > 0 {
		go maintenance.Schedule(ctx, cfg.Maintenance.CheckInterval)
	}

	// hub (WebSocket) + collecteur Prometheus alimentés par le même flux
	hub := httpapi.NewHub(logger)
	go hub.Run(ctx)
	collector := metrics.New(version)
	publisher := usecase.MultiPublisher{hub, collector}

	// alertes sortantes : troisième consommateur du même flux, ne retient
	// que les fins en échec. nil si aucune URL n'est configurée.
	alertFormat, err := notify.ParseFormat(cfg.Maintenance.AlertFormat)
	if err != nil {
		return fmt.Errorf("SDB_ALERT_FORMAT: %w", err)
	}
	alerts := notify.New(cfg.Maintenance.AlertWebhook, cfg.Maintenance.AlertTimeout, logger,
		notify.WithFormat(alertFormat))
	if alerts != nil {
		publisher = append(publisher, alerts)
		logger.Info("outbound alerting enabled", "format", string(alertFormat))
	} else {
		logger.Warn("no alert webhook configured; backup failures are only visible in /metrics and logs")
	}

	// copie secondaire (3-2-1) : le dépôt principal est un support unique tant
	// qu'aucun autre ne porte les mêmes snapshots
	replicationSvc := usecase.NewReplicationService(engine, storageRepo, publisher, logger,
		usecase.WithReplicationObserver(func(st usecase.ReplicationStatus) {
			collector.RecordReplication(st.CopyName, st.SourceName, st.Pending, st.Lag().Seconds())
		}))

	backupSvc := usecase.NewBackupService(runtime, engine, storageRepo, history, publisher, logger,
		usecase.WithStrictPartial(cfg.Maintenance.StrictPartial),
		usecase.WithReplicator(replicationSvc))
	restoreSvc := usecase.NewRestoreService(runtime, engine, storageRepo, restoreHistory, publisher, logger)

	// preuve de restaurabilite : extrait reellement le dernier snapshot dans
	// un volume jetable. Les echecs partent par le meme canal d'alerte que
	// n'importe quel run rate.
	verifySvc := usecase.NewVerificationService(runtime, engine, storageRepo, restoreHistory, publisher, logger)
	if cfg.Maintenance.VerifyInterval > 0 {
		go verifySvc.Schedule(ctx, cfg.Maintenance.VerifyInterval)
	} else {
		logger.Warn("restore verification disabled; nothing proves the backups are restorable (set SDB_VERIFY_INTERVAL)")
	}
	// une copie n'est prouvée que mesurée : la passe compare les snapshots des
	// deux dépôts et alimente sdb_replication_pending_snapshots
	if cfg.Maintenance.ReplicationInterval > 0 {
		go replicationSvc.Schedule(ctx, cfg.Maintenance.ReplicationInterval)
	}
	switch pairs, err := replicationSvc.Configured(ctx); {
	case err != nil:
		logger.Error("could not determine secondary copy configuration", "error", err)
	case pairs == 0:
		logger.Warn("no secondary copy configured; every backup lives on a single medium " +
			"(create a storage with copy_of_storage_id to satisfy the 3-2-1 rule)")
	default:
		logger.Info("secondary copies configured", "pairs", pairs,
			"reconciliation", cfg.Maintenance.ReplicationInterval.String())
	}

	schedulerSvc := usecase.NewSchedulerService(sqlite.NewScheduleRepo(db), backupSvc, logger,
		usecase.WithCatchUp(cfg.Maintenance.ScheduleCatchUp),
		usecase.WithMissedRunHandler(func(sched domain.BackupSchedule, missed int) {
			collector.RecordMissedRuns(sched.Name, sched.ContainerName, missed)
		}))
	go func() {
		if err := schedulerSvc.Run(ctx); err != nil {
			logger.Error("backup scheduler stopped", "error", err)
		}
	}()

	// --- API HTTP + frontend embarqué ---
	staticFS, err := web.Dist()
	if err != nil {
		logger.Warn("embedded frontend unavailable, serving API only (build it with `make web-build`)", "error", err)
	}
	if cfg.Auth.MetricsToken != "" {
		logger.Info("prometheus metrics enabled at /metrics")
	}

	server := httpapi.NewServer(httpapi.Options{
		Addr:         cfg.Server.Addr(),
		JWTSecret:    cfg.Auth.JWTSecret,
		TokenTTL:     cfg.Auth.TokenTTL,
		Version:      version,
		Static:       staticFS,
		Metrics:      collector.Handler(),
		MetricsToken: cfg.Auth.MetricsToken,
	}, httpapi.Services{
		Auth:        authSvc,
		Containers:  usecase.NewContainerService(runtime),
		Storages:    usecase.NewStorageService(storageRepo, engine, logger),
		Backups:     backupSvc,
		Restores:    restoreSvc,
		Scheduler:   schedulerSvc,
		Replication: replicationSvc,
	}, hub, logger)

	logger.Info("HTTP API listening", "addr", cfg.Server.Addr())
	serverErr := server.Run(ctx) // bloque jusqu'au signal d'arrêt

	// drain : annule les jobs et attend leurs rollbacks (redémarrages)
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelClose()
	if err := backupSvc.Close(closeCtx); err != nil {
		logger.Warn("backup jobs did not drain in time", "error", err)
	}
	if err := restoreSvc.Close(closeCtx); err != nil {
		logger.Warn("restore jobs did not drain in time", "error", err)
	}
	// après les services : les alertes des derniers rollbacks doivent partir
	if err := alerts.Close(closeCtx); err != nil {
		logger.Warn("pending alerts were not delivered", "error", err)
	}

	logger.Info("shutdown complete")
	return serverErr
}

func newLogger(cfg config.Log) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
