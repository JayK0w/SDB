package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// handleHealth : alimente l'indicateur North Star (SDB + Docker en un appel).
func (s *Server) handleHealth(c *gin.Context) {
	pingCtx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	dockerOK := s.svc.Containers.Ping(pingCtx) == nil

	status := "ok"
	if !dockerOK {
		status = "degraded"
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  status,
		"docker":  dockerOK,
		"version": s.version,
	})
}

func (s *Server) handleListContainers(c *gin.Context) {
	all := c.Query("all") == "true"
	containers, err := s.svc.Containers.List(c.Request.Context(), all)
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]containerDTO, 0, len(containers))
	for _, ct := range containers {
		out = append(out, toContainerDTO(ct))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleGetContainer(c *gin.Context) {
	container, err := s.svc.Containers.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toContainerDTO(*container))
}

// handleMetricsWS : upgrade et branchement du client sur le hub (auth déjà
// faite par le middleware, ?token= accepté pour les navigateurs).
func (s *Server) handleMetricsWS(c *gin.Context) {
	conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade a déjà écrit la réponse d'erreur HTTP
		s.logger.Warn("websocket upgrade failed", "error", err, "client", c.ClientIP())
		return
	}
	cl := &client{hub: s.hub, conn: conn, send: make(chan domain.ProgressEvent, clientBuffer)}
	s.hub.add(cl)
	go cl.writePump()
	go cl.readPump()
}
