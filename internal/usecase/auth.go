package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// MinPasswordLength follows NIST guidance: length over composition rules.
const MinPasswordLength = 12

// AuthService implements authentication and user management. Role-based
// authorization (admin-only endpoints) is enforced by the HTTP middleware;
// this service enforces the invariants that must hold regardless of the
// caller, such as never deleting or demoting the last admin.
type AuthService struct {
	users  domain.UserRepository
	hasher domain.PasswordHasher
	logger *slog.Logger

	dummyOnce sync.Once
	dummyHash string
}

func NewAuthService(users domain.UserRepository, hasher domain.PasswordHasher, logger *slog.Logger) *AuthService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthService{users: users, hasher: hasher, logger: logger}
}

// Login returns the user on success and domain.ErrUnauthorized for both
// unknown usernames and wrong passwords (no account enumeration). A dummy
// hash verification keeps the timing of the two cases comparable.
func (s *AuthService) Login(ctx context.Context, username, password string) (*domain.User, error) {
	u, err := s.users.GetByUsername(ctx, username)
	if errors.Is(err, domain.ErrNotFound) {
		if d := s.dummy(); d != "" {
			_, _ = s.hasher.Verify(password, d)
		}
		return nil, domain.ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("loading user: %w", err)
	}
	ok, err := s.hasher.Verify(password, u.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return nil, domain.ErrUnauthorized
	}
	return u, nil
}

func (s *AuthService) CreateUser(ctx context.Context, username, password string, role domain.Role) (*domain.User, error) {
	if username == "" {
		return nil, fmt.Errorf("%w: username is required", domain.ErrInvalidInput)
	}
	if !role.Valid() {
		return nil, fmt.Errorf("%w: unknown role %q", domain.ErrInvalidInput, role)
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}
	u := &domain.User{Username: username, PasswordHash: hash, Role: role}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	s.logger.Info("user created", "username", username, "role", role)
	return u, nil
}

func (s *AuthService) ListUsers(ctx context.Context) ([]domain.User, error) {
	return s.users.List(ctx)
}

func (s *AuthService) UpdatePassword(ctx context.Context, userID int64, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	u.PasswordHash = hash
	return s.users.Update(ctx, u)
}

func (s *AuthService) UpdateRole(ctx context.Context, userID int64, role domain.Role) error {
	if !role.Valid() {
		return fmt.Errorf("%w: unknown role %q", domain.ErrInvalidInput, role)
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.IsAdmin() && role != domain.RoleAdmin {
		if err := s.ensureNotLastAdmin(ctx, userID); err != nil {
			return err
		}
	}
	u.Role = role
	return s.users.Update(ctx, u)
}

func (s *AuthService) DeleteUser(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.IsAdmin() {
		if err := s.ensureNotLastAdmin(ctx, userID); err != nil {
			return err
		}
	}
	return s.users.Delete(ctx, userID)
}

// EnsureInitialAdmin creates the first admin account when the user table
// is empty. With an empty password a strong random one is generated and
// returned so the caller can surface it exactly once.
func (s *AuthService) EnsureInitialAdmin(ctx context.Context, username, password string) (created bool, generated string, err error) {
	n, err := s.users.Count(ctx)
	if err != nil {
		return false, "", fmt.Errorf("counting users: %w", err)
	}
	if n > 0 {
		return false, "", nil
	}
	if username == "" {
		username = "admin"
	}
	if password == "" {
		password, err = randomSecret(18) // 24 characters
		if err != nil {
			return false, "", err
		}
		generated = password
	}
	if _, err := s.CreateUser(ctx, username, password, domain.RoleAdmin); err != nil {
		return false, "", err
	}
	return true, generated, nil
}

func (s *AuthService) ensureNotLastAdmin(ctx context.Context, excludeID int64) error {
	all, err := s.users.List(ctx)
	if err != nil {
		return err
	}
	for _, u := range all {
		if u.IsAdmin() && u.ID != excludeID {
			return nil
		}
	}
	return fmt.Errorf("%w: at least one admin account must remain", domain.ErrForbidden)
}

// dummy lazily builds a hash used to equalize Login timing when the
// username does not exist.
func (s *AuthService) dummy() string {
	s.dummyOnce.Do(func() {
		if h, err := s.hasher.Hash("sdb-timing-equalizer"); err == nil {
			s.dummyHash = h
		}
	})
	return s.dummyHash
}

func validatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters", domain.ErrInvalidInput, MinPasswordLength)
	}
	return nil
}
