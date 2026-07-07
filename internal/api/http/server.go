package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/standalone-docker-backup/sdb/internal/usecase"
)

type Options struct {
	Addr      string
	JWTSecret string
	TokenTTL  time.Duration
	Version   string
	// Static, when set, is the built frontend served for non-API routes.
	Static fs.FS
	// Metrics, when set together with MetricsToken, exposes GET /metrics
	// behind the static bearer token (Prometheus-friendly auth).
	Metrics      http.Handler
	MetricsToken string
}

// Services groups the usecases the delivery layer exposes.
type Services struct {
	Auth       *usecase.AuthService
	Containers *usecase.ContainerService
	Storages   *usecase.StorageService
	Backups    *usecase.BackupService
	Restores   *usecase.RestoreService
	Scheduler  *usecase.SchedulerService
}

type Server struct {
	logger       *slog.Logger
	version      string
	hub          *Hub
	tokens       *TokenManager
	svc          Services
	upgrader     websocket.Upgrader
	loginLimiter *rateLimiter
	http         *http.Server
}

func NewServer(opts Options, svc Services, hub *Hub, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		logger:       logger,
		version:      opts.Version,
		hub:          hub,
		tokens:       NewTokenManager(opts.JWTSecret, opts.TokenTTL),
		svc:          svc,
		loginLimiter: newRateLimiter(10, time.Minute),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// Same-origin only: prevents cross-site WebSocket hijacking
			// from arbitrary pages while still allowing non-browser
			// clients (no Origin header).
			CheckOrigin: checkSameOrigin,
		},
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), s.requestLogger())

	api := r.Group("/api/v1")
	api.POST("/auth/login", s.handleLogin)

	authed := api.Group("", s.authRequired())
	{
		authed.GET("/health", s.handleHealth)
		authed.GET("/containers", s.handleListContainers)
		authed.GET("/containers/:id", s.handleGetContainer)

		authed.GET("/storage", s.handleListStorage)
		authed.GET("/storage/:id", s.handleGetStorage)
		authed.GET("/storage/:id/snapshots", s.handleListSnapshots)

		authed.POST("/backups", s.handleStartBackup)
		authed.DELETE("/backups/:id", s.handleCancelBackup)
		authed.GET("/backups/history", s.handleHistory)
		authed.GET("/backups/history/:id", s.handleHistoryRecord)

		authed.POST("/restores", s.handleStartRestore)
		authed.DELETE("/restores/:id", s.handleCancelRestore)
		authed.GET("/restores/history", s.handleRestoreHistory)

		authed.GET("/schedules", s.handleListSchedules)
		authed.GET("/schedules/:id", s.handleGetSchedule)
		authed.POST("/schedules", s.handleCreateSchedule)
		authed.PUT("/schedules/:id", s.handleUpdateSchedule)
		authed.DELETE("/schedules/:id", s.handleDeleteSchedule)
		authed.POST("/schedules/:id/run", s.handleRunSchedule)

		authed.GET("/ws/metrics", s.handleMetricsWS)

		// Password change is self-or-admin; the handler checks.
		authed.PUT("/users/:id/password", s.handleUpdatePassword)

		admin := authed.Group("", s.adminRequired())
		{
			admin.POST("/storage", s.handleCreateStorage)
			admin.PUT("/storage/:id", s.handleUpdateStorage)
			admin.DELETE("/storage/:id", s.handleDeleteStorage)
			admin.POST("/storage/:id/check", s.handleCheckStorage)

			admin.GET("/users", s.handleListUsers)
			admin.POST("/users", s.handleCreateUser)
			admin.PUT("/users/:id/role", s.handleUpdateRole)
			admin.DELETE("/users/:id", s.handleDeleteUser)
		}
	}

	// Prometheus exposition: disabled unless a token is configured — a
	// metrics endpoint open by default would leak operational detail.
	if opts.Metrics != nil && opts.MetricsToken != "" {
		r.GET("/metrics", s.staticTokenRequired(opts.MetricsToken), gin.WrapH(opts.Metrics))
	}

	if opts.Static != nil {
		r.NoRoute(s.spaHandler(opts.Static))
	}

	s.http = &http.Server{
		Addr:              opts.Addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second, // hijacked WebSocket conns are exempt
		IdleTimeout:       120 * time.Second,
	}
	return s
}

// Run serves until ctx is canceled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	}
}

func checkSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
