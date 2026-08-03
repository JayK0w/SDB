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
	Static    fs.FS // frontend embarqué, nil = API seule
	// Metrics + MetricsToken : GET /metrics derrière un token statique ;
	// token vide = endpoint absent (sécurisé par défaut).
	Metrics      http.Handler
	MetricsToken string
}

// Services : usecases exposés par la couche de livraison.
type Services struct {
	Auth        *usecase.AuthService
	Containers  *usecase.ContainerService
	Storages    *usecase.StorageService
	Backups     *usecase.BackupService
	Restores    *usecase.RestoreService
	Scheduler   *usecase.SchedulerService
	Replication *usecase.ReplicationService
}

type Server struct {
	logger       *slog.Logger
	version      string
	hub          *Hub
	tokens       *TokenManager
	svc          Services
	upgrader     websocket.Upgrader
	loginLimiter *rateLimiter
	writeLimit   *rateLimiter
	http         *http.Server
}

func NewServer(opts Options, svc Services, hub *Hub, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	// Sans AuthService, authRequired ne peut pas verifier la revocation de
	// session : on echoue au DEMARRAGE plutot qu'a la premiere requete. Une
	// dependance de securite absente doit etre un refus de demarrer, jamais
	// une verification silencieusement sautee.
	if svc.Auth == nil {
		panic("httpapi: Services.Auth is required — session revocation cannot be enforced without it")
	}
	s := &Server{
		logger:       logger,
		version:      opts.Version,
		hub:          hub,
		tokens:       NewTokenManager(opts.JWTSecret, opts.TokenTTL),
		svc:          svc,
		loginLimiter: newRateLimiter(10, time.Minute),
		// large devant un usage humain, étroit devant une boucle
		writeLimit: newRateLimiter(120, time.Minute),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// same-origin strict : bloque le hijacking WebSocket
			// cross-site ; Origin absent (client non-navigateur) accepté
			CheckOrigin: checkSameOrigin,
		},
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), s.requestLogger(), s.securityHeaders(), limitBody())

	api := r.Group("/api/v1")
	api.POST("/auth/login", s.handleLogin)

	// writeLimiter après authRequired : il préfère l'ID utilisateur à l'IP
	// comme clé, sinon tout un réseau NATé partage le même compteur.
	authed := api.Group("", s.authRequired(), s.writeLimiter())
	{
		authed.GET("/health", s.handleHealth)
		authed.GET("/containers", s.handleListContainers)
		authed.GET("/containers/:id", s.handleGetContainer)

		authed.GET("/storage", s.handleListStorage)
		authed.GET("/storage/:id", s.handleGetStorage)
		authed.GET("/storage/:id/snapshots", s.handleListSnapshots)
		authed.GET("/replication", s.handleReplicationStatus)

		authed.POST("/backups", s.handleStartBackup)
		authed.DELETE("/backups/:id", s.handleCancelBackup)
		authed.GET("/backups/history", s.handleHistory)
		authed.GET("/backups/history/:id", s.handleHistoryRecord)

		// lecture seule : tout compte authentifié
		authed.GET("/restores/history", s.handleRestoreHistory)

		authed.GET("/schedules", s.handleListSchedules)
		authed.GET("/schedules/:id", s.handleGetSchedule)
		authed.POST("/schedules", s.handleCreateSchedule)
		authed.PUT("/schedules/:id", s.handleUpdateSchedule)
		authed.DELETE("/schedules/:id", s.handleDeleteSchedule)
		authed.POST("/schedules/:id/run", s.handleRunSchedule)

		authed.GET("/ws/metrics", s.handleMetricsWS)

		// changement de mot de passe : soi-même ou admin (vérifié dans le handler)
		authed.PUT("/users/:id/password", s.handleUpdatePassword)
		// revocation : soi-meme ou admin (verifie dans le handler)
		authed.POST("/users/:id/revoke-sessions", s.handleRevokeSessions)

		admin := authed.Group("", s.adminRequired())
		{
			// Une restauration écrase des données de production, et un
			// clonage matérialise une copie complète : opérations
			// privilégiées, jamais ouvertes au rôle `user`.
			admin.POST("/restores", s.handleStartRestore)
			admin.DELETE("/restores/:id", s.handleCancelRestore)
			admin.GET("/restores/clone-compose", s.handleCloneCompose)

			admin.POST("/storage", s.handleCreateStorage)
			admin.PUT("/storage/:id", s.handleUpdateStorage)
			admin.DELETE("/storage/:id", s.handleDeleteStorage)
			admin.POST("/storage/:id/check", s.handleCheckStorage)
			// une copie complète consomme la bande passante des deux dépôts :
			// déclenchement réservé aux admins
			admin.POST("/storage/:id/replicate", s.handleReplicate)

			admin.GET("/users", s.handleListUsers)
			admin.POST("/users", s.handleCreateUser)
			admin.PUT("/users/:id/role", s.handleUpdateRole)
			admin.DELETE("/users/:id", s.handleDeleteUser)
		}
	}

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
		WriteTimeout:      60 * time.Second, // les connexions WS hijackées y échappent
		IdleTimeout:       120 * time.Second,
	}
	return s
}

// Run : sert jusqu'à annulation du contexte, puis arrêt gracieux.
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
