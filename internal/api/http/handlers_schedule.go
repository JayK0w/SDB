package httpapi

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func (s *Server) handleListSchedules(c *gin.Context) {
	schedules, err := s.svc.Scheduler.List(c.Request.Context())
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]scheduleDTO, 0, len(schedules))
	for _, sched := range schedules {
		out = append(out, toScheduleDTO(sched))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleGetSchedule(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	sched, err := s.svc.Scheduler.Get(c.Request.Context(), id)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toScheduleDTO(*sched))
}

func (s *Server) handleCreateSchedule(c *gin.Context) {
	var req scheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	sched := req.toDomain(0)
	if err := s.svc.Scheduler.Create(c.Request.Context(), sched); err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toScheduleDTO(*sched))
}

func (s *Server) handleUpdateSchedule(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	var req scheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	sched := req.toDomain(id)
	if err := s.svc.Scheduler.Update(c.Request.Context(), sched); err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toScheduleDTO(*sched))
}

func (s *Server) handleDeleteSchedule(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	if err := s.svc.Scheduler.Delete(c.Request.Context(), id); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// handleRunSchedule : déclenchement manuel immédiat (202 + enregistrement).
func (s *Server) handleRunSchedule(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	rec, err := s.svc.Scheduler.RunNow(c.Request.Context(), id, currentActor(c))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, toRecordDTO(*rec))
}
