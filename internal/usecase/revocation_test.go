package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func revocationFixture(t *testing.T) (*AuthService, *memUsers, int64) {
	t.Helper()
	users := newMemUsers()
	svc := NewAuthService(users, fakeHasher{}, discardLogger())
	u, err := svc.CreateUser(context.Background(), "alice", "correct-horse-battery", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	return svc, users, u.ID
}

// Un compte neuf demarre a la generation 1, pas 0 : le zero de Go serait
// indistinguable d'un champ jamais renseigne.
func TestNewUserStartsAtVersionOne(t *testing.T) {
	_, users, id := revocationFixture(t)
	v, err := users.TokenVersion(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("TokenVersion = %d, want 1", v)
	}
}

// Changer son mot de passe deconnecte partout : c'est le geste attendu apres
// une fuite d'identifiants.
func TestPasswordChangeRevokesSessions(t *testing.T) {
	svc, users, id := revocationFixture(t)
	ctx := context.Background()
	before, _ := users.TokenVersion(ctx, id)

	if err := svc.UpdatePassword(ctx, id, "another-long-password"); err != nil {
		t.Fatal(err)
	}
	after, _ := users.TokenVersion(ctx, id)
	if after <= before {
		t.Fatalf("version %d -> %d : le changement de mot de passe doit revoquer", before, after)
	}
	// une session emise avant n'est plus valide
	if err := svc.ValidateSession(ctx, id, before); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("ancienne session = %v, want ErrUnauthorized", err)
	}
}

// Retirer un role doit couper les sessions : un jeton porte le role au moment
// de son emission, sinon l'ancien admin le reste jusqu'a expiration.
func TestRoleChangeRevokesSessions(t *testing.T) {
	svc, users, id := revocationFixture(t)
	ctx := context.Background()
	before, _ := users.TokenVersion(ctx, id)

	if err := svc.UpdateRole(ctx, id, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	after, _ := users.TokenVersion(ctx, id)
	if after <= before {
		t.Fatalf("version %d -> %d : le changement de role doit revoquer", before, after)
	}
}

func TestRevokeSessionsBumpsVersion(t *testing.T) {
	svc, users, id := revocationFixture(t)
	ctx := context.Background()
	before, _ := users.TokenVersion(ctx, id)

	if err := svc.RevokeSessions(ctx, id); err != nil {
		t.Fatal(err)
	}
	after, _ := users.TokenVersion(ctx, id)
	if after != before+1 {
		t.Fatalf("version %d -> %d, want +1", before, after)
	}
	if err := svc.ValidateSession(ctx, id, after); err != nil {
		t.Fatalf("la generation courante doit rester valide : %v", err)
	}
}

// Un compte supprime n'a plus de generation : ses jetons doivent etre
// refuses sans qu'on ait eu a les revoquer un par un.
func TestValidateSessionRejectsDeletedAccount(t *testing.T) {
	svc, users, id := revocationFixture(t)
	ctx := context.Background()
	v, _ := users.TokenVersion(ctx, id)

	if err := users.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.ValidateSession(ctx, id, v); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("compte supprime = %v, want ErrUnauthorized", err)
	}
}

func TestValidateSessionAcceptsCurrentVersion(t *testing.T) {
	svc, users, id := revocationFixture(t)
	ctx := context.Background()
	v, _ := users.TokenVersion(ctx, id)

	if err := svc.ValidateSession(ctx, id, v); err != nil {
		t.Fatalf("ValidateSession() = %v, want nil", err)
	}
}

// La revocation ne doit toucher QUE le compte vise.
func TestRevocationIsScopedToOneAccount(t *testing.T) {
	svc, users, alice := revocationFixture(t)
	ctx := context.Background()
	bob, err := svc.CreateUser(ctx, "bob", "correct-horse-battery", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	bobBefore, _ := users.TokenVersion(ctx, bob.ID)

	if err := svc.RevokeSessions(ctx, alice); err != nil {
		t.Fatal(err)
	}
	bobAfter, _ := users.TokenVersion(ctx, bob.ID)
	if bobAfter != bobBefore {
		t.Fatalf("la session de bob a ete revoquee par ricochet : %d -> %d", bobBefore, bobAfter)
	}
	if err := svc.ValidateSession(ctx, bob.ID, bobBefore); err != nil {
		t.Fatalf("bob doit rester connecte : %v", err)
	}
}
