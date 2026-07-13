package crypto

import (
	"bytes"
	"strings"
	"testing"
)

const testMasterKey = "0123456789abcdef0123456789abcdef" // 32 caractères

func TestArgon2idRoundTrip(t *testing.T) {
	h := NewArgon2idHasher()
	encoded, err := h.Hash("s3cret-passw0rd")
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("unexpected hash format: %s", encoded)
	}
	ok, err := h.Verify("s3cret-passw0rd", encoded)
	if err != nil || !ok {
		t.Fatalf("Verify(correct) = %v, %v; want true, nil", ok, err)
	}
	ok, err = h.Verify("wrong-password", encoded)
	if err != nil || ok {
		t.Fatalf("Verify(wrong) = %v, %v; want false, nil", ok, err)
	}
}

func TestArgon2idUniqueSalts(t *testing.T) {
	h := NewArgon2idHasher()
	a, _ := h.Hash("same-password")
	b, _ := h.Hash("same-password")
	if a == b {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

func TestArgon2idMalformed(t *testing.T) {
	h := NewArgon2idHasher()
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$abc$def", "$argon2id$v=19$m=1,t=1,p=1$!!$??"} {
		if _, err := h.Verify("x", bad); err == nil {
			t.Errorf("Verify(%q) accepted a malformed hash", bad)
		}
	}
}

func TestAESGCMRoundTrip(t *testing.T) {
	c, err := NewAESGCM(testMasterKey)
	if err != nil {
		t.Fatalf("NewAESGCM() error: %v", err)
	}
	plaintext := []byte(`{"AWS_SECRET_ACCESS_KEY":"very-secret"}`)
	sealed, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}
	if bytes.Contains(sealed, []byte("very-secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	opened, err := c.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt() error: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("round trip mismatch: got %q", opened)
	}
}

func TestAESGCMNonceUniqueness(t *testing.T) {
	c, _ := NewAESGCM(testMasterKey)
	a, _ := c.Encrypt([]byte("same"))
	b, _ := c.Encrypt([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext must differ (random nonce)")
	}
}

func TestAESGCMTamperDetection(t *testing.T) {
	c, _ := NewAESGCM(testMasterKey)
	sealed, _ := c.Encrypt([]byte("payload"))
	sealed[len(sealed)-1] ^= 0xFF
	if _, err := c.Decrypt(sealed); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestAESGCMWrongKey(t *testing.T) {
	a, _ := NewAESGCM(testMasterKey)
	b, _ := NewAESGCM("another-master-key-of-32-chars!!")
	sealed, _ := a.Encrypt([]byte("payload"))
	if _, err := b.Decrypt(sealed); err == nil {
		t.Fatal("ciphertext decrypted with the wrong key")
	}
}

func TestAESGCMShortKeyRejected(t *testing.T) {
	if _, err := NewAESGCM("too-short"); err == nil {
		t.Fatal("short master key accepted")
	}
}
