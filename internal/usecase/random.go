package usecase

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// randomSecret : chaîne aléatoire URL-safe (~4n/3 caractères) — mots de
// passe restic générés et mot de passe admin de bootstrap.
func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
