package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func (s *Server) handleListStorage(c *gin.Context) {
	configs, err := s.svc.Storages.List(c.Request.Context())
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]storageDTO, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, toStorageDTO(cfg))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleGetStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	cfg, err := s.svc.Storages.Get(c.Request.Context(), id)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStorageDTO(*cfg))
}

func (s *Server) handleCreateStorage(c *gin.Context) {
	var req storageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	cfg := req.toDomain(0)
	if err := s.svc.Storages.Create(c.Request.Context(), cfg); err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toStorageDTO(*cfg))
}

func (s *Server) handleUpdateStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	var req storageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	cfg := req.toDomain(id)
	if err := s.svc.Storages.Update(c.Request.Context(), cfg); err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStorageDTO(*cfg))
}

func (s *Server) handleDeleteStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	if err := s.svc.Storages.Delete(c.Request.Context(), id); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleListSnapshots(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	snapshots, err := s.svc.Storages.Snapshots(c.Request.Context(), id, c.QueryArray("tag"))
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]snapshotDTO, 0, len(snapshots))
	for _, snap := range snapshots {
		out = append(out, toSnapshotDTO(snap))
	}
	c.JSON(http.StatusOK, out)
}

// handleCheckStorage : restic check peut durer des minutes → exécution en
// arrière-plan, résultat via le flux d'événements et les logs (202).
func (s *Server) handleCheckStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	// existence vérifiée avant d'accepter le job
	if _, err := s.svc.Storages.Get(c.Request.Context(), id); err != nil {
		s.respondError(c, err)
		return
	}
	go func() {
		checkCtx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if err := s.svc.Storages.CheckIntegrity(checkCtx, id); err != nil {
			s.logger.Error("on-demand integrity check failed", "storage_id", id, "error", err)
			s.hub.Publish(domain.ProgressEvent{
				Type:    domain.EventError,
				Time:    time.Now().UTC(),
				Message: fmt.Sprintf("integrity check of storage %d failed: %v", id, err),
			})
			return
		}
		s.hub.Publish(domain.ProgressEvent{
			Type:    domain.EventLog,
			Time:    time.Now().UTC(),
			Message: fmt.Sprintf("integrity check of storage %d passed", id),
		})
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}
