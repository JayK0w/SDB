package usecase

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// randomSecret returns a URL-safe random string built from n bytes of
// entropy (~4n/3 characters). Used for generated Restic repository
// passwords and the bootstrap admin password.
func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
