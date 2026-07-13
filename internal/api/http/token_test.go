package httpapi

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestTokenRoundTrip(t *testing.T) {
	mgr := NewTokenManager(testSecret, time.Hour)
	user := &domain.User{ID: 7, Username: "alice", Role: domain.RoleAdmin}

	token, expiresAt, err := mgr.Issue(user)
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if !expiresAt.After(time.Now()) {
		t.Fatal("expiry must be in the future")
	}

	claims, err := mgr.Parse(token)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if claims.UserID() != 7 || claims.Username != "alice" || !claims.IsAdmin() {
		t.Fatalf("claims mismatch: %+v", claims)
	}
}

func TestTokenExpired(t *testing.T) {
	mgr := NewTokenManager(testSecret, -time.Minute)
	token, _, err := mgr.Issue(&domain.User{ID: 1, Username: "x", Role: domain.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Parse(token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Parse(expired) err = %v, want ErrUnauthorized", err)
	}
}

func TestTokenWrongSecret(t *testing.T) {
	a := NewTokenManager(testSecret, time.Hour)
	b := NewTokenManager("another-secret-of-32-characters!", time.Hour)
	token, _, _ := a.Issue(&domain.User{ID: 1, Username: "x", Role: domain.RoleUser})
	if _, err := b.Parse(token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Parse(wrong secret) err = %v, want ErrUnauthorized", err)
	}
}

func TestTokenGarbageRejected(t *testing.T) {
	mgr := NewTokenManager(testSecret, time.Hour)
	for _, bad := range []string{"", "garbage", "a.b.c"} {
		if _, err := mgr.Parse(bad); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("Parse(%q) err = %v, want ErrUnauthorized", bad, err)
		}
	}
}

// prouve l epinglage HS256 : un token signe alg=none ne doit jamais
// passer (attaque classique par confusion d algorithme JWT)
func TestTokenAlgNoneRejected(t *testing.T) {
	mgr := NewTokenManager(testSecret, time.Hour)
	claims := &Claims{
		Username: "attacker",
		Role:     string(domain.RoleAdmin),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "sdb",
			Subject:   "1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Parse(token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Parse(alg=none) err = %v, want ErrUnauthorized", err)
	}
}
