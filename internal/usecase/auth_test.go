package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func newAuthFixture() (*AuthService, *memUsers) {
	users := newMemUsers()
	return NewAuthService(users, fakeHasher{}, discardLogger()), users
}

func TestEnsureInitialAdminAndLogin(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAuthFixture()

	created, generated, err := svc.EnsureInitialAdmin(ctx, "admin", "")
	if err != nil || !created {
		t.Fatalf("EnsureInitialAdmin() = %v, %v; want created", created, err)
	}
	if len(generated) < MinPasswordLength {
		t.Fatalf("generated password %q too short", generated)
	}

	u, err := svc.Login(ctx, "admin", generated)
	if err != nil || !u.IsAdmin() {
		t.Fatalf("Login(generated password) = %+v, %v", u, err)
	}

	// Second call must be a no-op once a user exists.
	created, _, err = svc.EnsureInitialAdmin(ctx, "admin", "")
	if err != nil || created {
		t.Fatalf("second EnsureInitialAdmin() = %v, %v; want no-op", created, err)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAuthFixture()
	if _, _, err := svc.EnsureInitialAdmin(ctx, "admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Login(ctx, "admin", "wrong-password"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Login(wrong password) err = %v, want ErrUnauthorized", err)
	}
	if _, err := svc.Login(ctx, "ghost", "whatever"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Login(unknown user) err = %v, want ErrUnauthorized", err)
	}
}

func TestCreateUserValidation(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAuthFixture()

	if _, err := svc.CreateUser(ctx, "bob", "short", domain.RoleUser); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("short password err = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.CreateUser(ctx, "bob", "long-enough-password", "superuser"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("bad role err = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.CreateUser(ctx, "bob", "long-enough-password", domain.RoleUser); err != nil {
		t.Fatalf("valid user rejected: %v", err)
	}
	if _, err := svc.CreateUser(ctx, "bob", "long-enough-password", domain.RoleUser); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate username err = %v, want ErrAlreadyExists", err)
	}
}

func TestLastAdminIsProtected(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAuthFixture()

	admin, err := svc.CreateUser(ctx, "admin", "long-enough-password", domain.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUser(ctx, admin.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("deleting the last admin err = %v, want ErrForbidden", err)
	}
	if err := svc.UpdateRole(ctx, admin.ID, domain.RoleUser); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("demoting the last admin err = %v, want ErrForbidden", err)
	}

	second, err := svc.CreateUser(ctx, "admin2", "long-enough-password", domain.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUser(ctx, admin.ID); err != nil {
		t.Fatalf("deleting an admin with another one left: %v", err)
	}
	if err := svc.UpdateRole(ctx, second.ID, domain.RoleUser); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("demoting the now-last admin err = %v, want ErrForbidden", err)
	}
}

func TestUpdatePassword(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAuthFixture()
	u, err := svc.CreateUser(ctx, "alice", "initial-password-123", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdatePassword(ctx, u.ID, "tiny"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("short new password err = %v, want ErrInvalidInput", err)
	}
	if err := svc.UpdatePassword(ctx, u.ID, "brand-new-password-456"); err != nil {
		t.Fatalf("UpdatePassword() error: %v", err)
	}
	if _, err := svc.Login(ctx, "alice", "initial-password-123"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("old password still accepted")
	}
	if _, err := svc.Login(ctx, "alice", "brand-new-password-456"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}
