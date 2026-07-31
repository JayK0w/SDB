package httpapi

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// handleStartBackup : 202 immédiat, progression via /ws/metrics, état
// final dans l'historique.
func (s *Server) handleStartBackup(c *gin.Context) {
	var req backupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	dom := req.toDomain()
	dom.TriggeredBy = currentActor(c)
	rec, err := s.svc.Backups.Start(c.Request.Context(), dom)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, toRecordDTO(*rec))
}

func (s *Server) handleCancelBackup(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	if err := s.svc.Backups.Cancel(id); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleHistory(c *gin.Context) {
	filter := domain.HistoryFilter{
		ContainerID: c.Query("container_id"),
		Status:      domain.BackupStatus(c.Query("status")),
	}
	for name, dst := range map[string]*int{"limit": &filter.Limit, "offset": &filter.Offset} {
		if v := c.Query(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				s.respondError(c, fmt.Errorf("%w: %s must be a positive integer", domain.ErrInvalidInput, name))
				return
			}
			*dst = n
		}
	}
	if v := c.Query("storage_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			s.respondError(c, fmt.Errorf("%w: storage_id must be a positive integer", domain.ErrInvalidInput))
			return
		}
		filter.StorageID = n
	}

	records, err := s.svc.Backups.History(c.Request.Context(), filter)
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]backupRecordDTO, 0, len(records))
	for _, rec := range records {
		out = append(out, toRecordDTO(rec))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleHistoryRecord(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	rec, err := s.svc.Backups.GetRecord(c.Request.Context(), id)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toRecordDTO(*rec))
}

func (s *Server) handleStartRestore(c *gin.Context) {
	var req restoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	dom := req.toDomain()
	dom.TriggeredBy = currentActor(c)
	rec, err := s.svc.Restores.Start(c.Request.Context(), dom)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, toRestoreDTO(*rec))
}

// handleCloneCompose : docker-compose.yml pour lancer un second service sur
// le volume clone, a cote de l original qui continue de tourner.
func (s *Server) handleCloneCompose(c *gin.Context) {
	yaml, err := s.svc.Restores.CloneCompose(c.Request.Context(),
		c.Query("container_id"), c.Query("source_volume"), c.Query("target_volume"))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"compose": yaml})
}

func (s *Server) handleCancelRestore(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	if err := s.svc.Restores.Cancel(id); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleRestoreHistory(c *gin.Context) {
	filter := domain.RestoreFilter{
		TargetVolume: c.Query("target_volume"),
		Status:       domain.BackupStatus(c.Query("status")),
	}
	for name, dst := range map[string]*int{"limit": &filter.Limit, "offset": &filter.Offset} {
		if v := c.Query(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				s.respondError(c, fmt.Errorf("%w: %s must be a positive integer", domain.ErrInvalidInput, name))
				return
			}
			*dst = n
		}
	}
	if v := c.Query("storage_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			s.respondError(c, fmt.Errorf("%w: storage_id must be a positive integer", domain.ErrInvalidInput))
			return
		}
		filter.StorageID = n
	}

	records, err := s.svc.Restores.History(c.Request.Context(), filter)
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]restoreRecordDTO, 0, len(records))
	for _, rec := range records {
		out = append(out, toRestoreDTO(rec))
	}
	c.JSON(http.StatusOK, out)
}
