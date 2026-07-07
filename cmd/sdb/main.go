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
	"github.com/standalone-docker-backup/sdb/internal/infra/crypto"
	"github.com/standalone-docker-backup/sdb/internal/infra/docker"
	"github.com/standalone-docker-backup/sdb/internal/infra/restic"
	"github.com/standalone-docker-backup/sdb/internal/infra/sqlite"
	"github.com/standalone-docker-backup/sdb/internal/usecase"
	"github.com/standalone-docker-backup/sdb/web"
)

// version is injected at build time (see Makefile LDFLAGS).
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting SDB", "version", version, "listen", cfg.Server.Addr())
	if !cfg.Server.IsLoopback() {
		logger.Warn("listen address is not loopback-only; if this port is published by Docker, "+
			"bind it on the host side as 127.0.0.1:PORT:PORT — Docker port publishing bypasses UFW/iptables",
			"host", cfg.Server.Host)
	}

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

	// Runs left pending/running by a crash or restart can never finish:
	// mark them failed before accepting new work.
	history := sqlite.NewHistoryRepo(db)
	if n, err := history.FailInterrupted(ctx, "interrupted by SDB restart"); err != nil {
		return fmt.Errorf("cleaning interrupted runs: %w", err)
	} else if n > 0 {
		logger.Warn("marked interrupted backup runs as failed", "count", n)
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
	engine := restic.New(runtime, cfg.Docker.WorkerImage)

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

	// The hub is the EventPublisher every long-running usecase writes to.
	hub := httpapi.NewHub(logger)
	go hub.Run(ctx)

	backupSvc := usecase.NewBackupService(runtime, engine, storageRepo, history, hub, logger)
	restoreSvc := usecase.NewRestoreService(runtime, engine, storageRepo, hub, logger)

	staticFS, err := web.Dist()
	if err != nil {
		logger.Warn("embedded frontend unavailable, serving API only (build it with `make web-build`)", "error", err)
	}

	server := httpapi.NewServer(httpapi.Options{
		Addr:      cfg.Server.Addr(),
		JWTSecret: cfg.Auth.JWTSecret,
		TokenTTL:  cfg.Auth.TokenTTL,
		Version:   version,
		Static:    staticFS,
	}, httpapi.Services{
		Auth:       authSvc,
		Containers: usecase.NewContainerService(runtime),
		Storages:   usecase.NewStorageService(storageRepo, engine, logger),
		Backups:    backupSvc,
		Restores:   restoreSvc,
	}, hub, logger)

	logger.Info("HTTP API listening", "addr", cfg.Server.Addr())
	serverErr := server.Run(ctx) // blocks until shutdown signal or listen failure

	// Drain running jobs: cancel them and wait for their rollback paths
	// (container restarts) to complete before releasing the process.
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelClose()
	if err := backupSvc.Close(closeCtx); err != nil {
		logger.Warn("backup jobs did not drain in time", "error", err)
	}
	if err := restoreSvc.Close(closeCtx); err != nil {
		logger.Warn("restore jobs did not drain in time", "error", err)
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
