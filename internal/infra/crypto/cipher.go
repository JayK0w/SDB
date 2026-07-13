package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// octet de version en préfixe : permet de changer d'algo sans casser l'existant
const cipherVersion byte = 1

// AESGCM : chiffrement des secrets au repos. Clé 256 bits dérivée de la
// clé maître par SHA-256 (la config impose ≥ 32 caractères d'entropie).
type AESGCM struct {
	aead cipher.AEAD
}

var _ domain.Cipher = (*AESGCM)(nil)

func NewAESGCM(masterKey string) (*AESGCM, error) {
	if len(masterKey) < 32 {
		return nil, errors.New("master key must be at least 32 characters")
	}
	key := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AESGCM{aead: aead}, nil
}

// Encrypt : version || nonce aléatoire || données scellées (GCM authentifie).
func (c *AESGCM) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	out = append(out, cipherVersion)
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, plaintext, nil), nil
}

func (c *AESGCM) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 1+c.aead.NonceSize()+c.aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	if ciphertext[0] != cipherVersion {
		return nil, fmt.Errorf("unsupported ciphertext version %d", ciphertext[0])
	}
	nonce := ciphertext[1 : 1+c.aead.NonceSize()]
	data := ciphertext[1+c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, data, nil)
	if err != nil {
		// message volontairement générique : ne révèle pas si la clé ou
		// la donnée est en cause
		return nil, errors.New("decryption failed: wrong master key or corrupted data")
	}
	return plaintext, nil
}
