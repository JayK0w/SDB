package httpapi

import (
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
			return // long-lived connection, duration is meaningless
		}
		s.logger.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client", c.ClientIP())
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
		c.Set(userContextKey, claims)
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

// bearerToken reads the Authorization header, falling back to the token
// query parameter: browsers cannot set headers on WebSocket dials.
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

// respondError maps domain sentinels onto HTTP statuses. Unclassified
// errors become an opaque 500: the detail goes to the log, not the wire.
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

// rateLimiter is a fixed-window counter, enough to blunt online
// password-guessing against the login endpoint.
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
