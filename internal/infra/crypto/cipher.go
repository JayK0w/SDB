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

// cipherVersion prefixes every ciphertext so the format can evolve
// (algorithm change, KDF change) without breaking stored secrets.
const cipherVersion byte = 1

// AESGCM implements domain.Cipher with AES-256-GCM. The 256-bit key is
// derived from the master key material with SHA-256; the configuration
// layer enforces a high-entropy master key (>= 32 characters), so a
// memory-hard KDF is not needed here.
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

// Encrypt returns version || nonce || sealed. A fresh random nonce is
// drawn per call; GCM authenticates the ciphertext, so any tampering is
// detected at decryption time.
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
		// Deliberately generic: do not leak whether the key or the data
		// is at fault.
		return nil, errors.New("decryption failed: wrong master key or corrupted data")
	}
	return plaintext, nil
}
