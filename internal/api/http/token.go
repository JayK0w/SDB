package httpapi

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// Claims is the JWT payload issued at login. The user ID travels in the
// registered Subject claim.
type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func (c *Claims) UserID() int64 {
	id, _ := strconv.ParseInt(c.Subject, 10, 64)
	return id
}

func (c *Claims) IsAdmin() bool { return domain.Role(c.Role) == domain.RoleAdmin }

// TokenManager issues and validates HMAC-SHA256 signed access tokens.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
}

func NewTokenManager(secret string, ttl time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), ttl: ttl}
}

func (m *TokenManager) Issue(user *domain.User) (token string, expiresAt time.Time, err error) {
	now := time.Now().UTC()
	expiresAt = now.Add(m.ttl)
	claims := &Claims{
		Username: user.Username,
		Role:     string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "sdb",
			Subject:   strconv.FormatInt(user.ID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing token: %w", err)
	}
	return token, expiresAt, nil
}

// Parse validates the signature, algorithm (pinned to HS256 to rule out
// algorithm-confusion attacks), issuer and expiry. Every failure maps to
// ErrUnauthorized without detail: token errors are not the client's
// business to distinguish.
func (m *TokenManager) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims,
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer("sdb"),
	)
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("%w: invalid or expired token", domain.ErrUnauthorized)
	}
	return claims, nil
}
