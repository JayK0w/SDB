package domain

import (
	"fmt"
	"time"
)

// Role controls what an account may do. Admins manage users and storage
// configurations; regular users trigger and monitor backups.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

func (r Role) Valid() bool { return r == RoleAdmin || r == RoleUser }

// User is an operator account. PasswordHash always holds an Argon2id
// encoded hash; plaintext passwords never cross the domain boundary
// outside of the authentication usecase.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

func (u *User) Validate() error {
	if u.Username == "" {
		return fmt.Errorf("%w: username is required", ErrInvalidInput)
	}
	if u.PasswordHash == "" {
		return fmt.Errorf("%w: password hash is required", ErrInvalidInput)
	}
	if !u.Role.Valid() {
		return fmt.Errorf("%w: unknown role %q", ErrInvalidInput, u.Role)
	}
	return nil
}
