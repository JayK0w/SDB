package httpapi

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func (s *Server) handleLogin(c *gin.Context) {
	if !s.loginLimiter.allow(c.ClientIP()) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts, retry in a minute"})
		return
	}
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	user, err := s.svc.Auth.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		s.respondError(c, err)
		return
	}
	token, expiresAt, err := s.tokens.Issue(user)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":      token,
		"expires_at": expiresAt,
		"user":       toUserDTO(*user),
	})
}

func (s *Server) handleListUsers(c *gin.Context) {
	users, err := s.svc.Auth.ListUsers(c.Request.Context())
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, toUserDTO(u))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleCreateUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	user, err := s.svc.Auth.CreateUser(c.Request.Context(), req.Username, req.Password, domain.Role(req.Role))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toUserDTO(*user))
}

// handleUpdatePassword lets a user change their own password; admins can
// change anyone's.
func (s *Server) handleUpdatePassword(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	claims := currentClaims(c)
	if claims == nil || (!claims.IsAdmin() && claims.UserID() != id) {
		s.respondError(c, fmt.Errorf("%w: you may only change your own password", domain.ErrForbidden))
		return
	}
	var req updatePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	if err := s.svc.Auth.UpdatePassword(c.Request.Context(), id, req.Password); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleUpdateRole(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	var req updateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	if err := s.svc.Auth.UpdateRole(c.Request.Context(), id, domain.Role(req.Role)); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	if err := s.svc.Auth.DeleteUser(c.Request.Context(), id); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
