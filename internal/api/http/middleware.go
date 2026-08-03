package httpapi

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

const userContextKey = "sdb.claims"

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if strings.HasSuffix(c.FullPath(), "/ws/metrics") {
			return // connexion longue durée, durée sans intérêt
		}
		s.logger.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client", c.ClientIP())
	}
}

// maxBodyBytes : plafond du corps des requêtes. Les payloads de SDB sont des
// documents JSON de configuration, jamais des téléversements — sans plafond,
// un client authentifié peut faire gonfler la mémoire du processus à volonté.
const maxBodyBytes = 1 << 20 // 1 Mio

// securityHeaders : l'UI est servie depuis le même binaire, la CSP peut donc
// être stricte. `default-src 'self'` bloque toute ressource tierce ;
// 'unsafe-inline' sur style-src reste nécessaire aux styles générés par Vue.
// connect-src autorise le WebSocket de progression sur la même origine.
func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self' data:; "+
				"connect-src 'self' ws: wss:; "+
				"object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		// HSTS seulement sous TLS : l'envoyer en clair sur un déploiement
		// loopback verrouillerait le navigateur sur un https inexistant.
		if c.Request.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

// limitBody : borne le corps avant tout décodage JSON.
func limitBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		}
		c.Next()
	}
}

// writeLimiter : plafond sur les opérations mutantes. Les runs sont déjà
// protégés du parallélisme par ErrConflict, mais rien n'empêchait un compte
// valide de marteler l'API et de saturer le daemon Docker.
func (s *Server) writeLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		key := c.ClientIP()
		if claims := currentClaims(c); claims != nil {
			key = strconv.FormatInt(claims.UserID(), 10)
		}
		if !s.writeLimit.allow(key) {
			s.respondError(c, fmt.Errorf("%w: too many write requests, slow down", domain.ErrConflict))
			return
		}
		c.Next()
	}
}

func (s *Server) authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			s.respondError(c, fmt.Errorf("%w: missing credentials", domain.ErrUnauthorized))
			return
		}
		claims, err := s.tokens.Parse(token)
		if err != nil {
			s.respondError(c, err)
			return
		}
		// La signature prouve seulement que le jeton a été émis par nous.
		// Elle ne dit rien de ce qui s'est passé DEPUIS : compte supprimé,
		// rôle retiré, mot de passe changé, sessions révoquées. D'où cette
		// vérification de génération à chaque requête.
		//
		// Coût assumé : une lecture SQLite locale d'un entier par requête
		// authentifiée. Un cache la rendrait gratuite mais réintroduirait un
		// délai de révocation — précisément ce qu'on supprime ici.
		if err := s.svc.Auth.ValidateSession(c.Request.Context(), claims.UserID(), claims.Ver); err != nil {
			s.respondError(c, err)
			return
		}
		c.Set(userContextKey, claims)
		c.Next()
	}
}

// staticTokenRequired : token fixe comparé en temps constant (Prometheus
// ne sait pas rafraîchir un JWT).
func (s *Server) staticTokenRequired(token string) gin.HandlerFunc {
	want := []byte(token)
	return func(c *gin.Context) {
		got := []byte(bearerToken(c))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			s.respondError(c, fmt.Errorf("%w: invalid metrics token", domain.ErrUnauthorized))
			return
		}
		c.Next()
	}
}

func (s *Server) adminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := currentClaims(c)
		if claims == nil || !claims.IsAdmin() {
			s.respondError(c, fmt.Errorf("%w: admin role required", domain.ErrForbidden))
			return
		}
		c.Next()
	}
}

// bearerToken : header Authorization, sinon ?token= (les navigateurs ne
// posent pas de headers sur un dial WebSocket).
func bearerToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return c.Query("token")
}

func currentClaims(c *gin.Context) *Claims {
	v, ok := c.Get(userContextKey)
	if !ok {
		return nil
	}
	claims, _ := v.(*Claims)
	return claims
}

// currentActor : auteur de la requête, pour l'attribution des runs. Ne peut
// être atteint que derrière authRequired ; l'acteur anonyme signalerait un
// câblage de route fautif plutôt qu'un appel légitime.
func currentActor(c *gin.Context) domain.Actor {
	claims := currentClaims(c)
	if claims == nil {
		return domain.Actor{Name: "unknown"}
	}
	return domain.Actor{UserID: claims.UserID(), Name: claims.Username}
}

// respondError : sentinelles domaine → codes HTTP ; erreur inconnue = 500
// opaque, le détail part dans les logs, pas sur le réseau.
func (s *Server) respondError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, domain.ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		s.logger.Error("internal error", "path", c.Request.URL.Path, "error", err)
		msg = "internal error"
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func pathID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: invalid id %q", domain.ErrInvalidInput, c.Param("id"))
	}
	return id, nil
}

// rateLimiter : fenêtre fixe, suffisant contre le guessing de mots de
// passe sur /auth/login.
type rateLimiter struct {
	mu       sync.Mutex
	limit    int
	interval time.Duration
	window   time.Time
	counts   map[string]int
}

func newRateLimiter(limit int, interval time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, interval: interval, counts: map[string]int{}}
}

func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.window) > r.interval {
		r.window = now
		clear(r.counts)
	}
	r.counts[key]++
	return r.counts[key] <= r.limit
}
