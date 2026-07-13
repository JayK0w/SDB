// Package crypto : hachage Argon2id (mots de passe) et AES-256-GCM
// (secrets au repos).
package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// Paramètres OWASP (64 Mio, 3 itérations) — encodés dans le hash, donc
// durcissables plus tard sans invalider l'existant.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // Kio
	argonThreads = 2
	argonSaltLen = 16
	argonKeyLen  = 32
)

type Argon2idHasher struct{}

var _ domain.PasswordHasher = (*Argon2idHasher)(nil)

func NewArgon2idHasher() *Argon2idHasher { return &Argon2idHasher{} }

// Hash : format encodé standard $argon2id$v=19$m=...,t=...,p=...$sel$empreinte
func (h *Argon2idHasher) Hash(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify : recalcule avec les paramètres du hash, compare en temps constant.
// (false, nil) = mauvais mot de passe ; erreur = hash malformé.
func (h *Argon2idHasher) Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("malformed argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("malformed argon2id version: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version %d", version)
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false, fmt.Errorf("malformed argon2id parameters: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("malformed argon2id salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("malformed argon2id digest: %w", err)
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
