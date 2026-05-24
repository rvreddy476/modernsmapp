// Package crypto wraps AES-256-GCM for at-rest secret storage.
//
// Threat model: an attacker who gains read access to the Postgres
// database (SQL injection, leaked pg_dump, compromised admin user)
// should not be able to recover TOTP secrets without also having
// read access to the application's environment (where the key lives).
//
// Usage:
//
//	box, _ := crypto.NewSecretBox(os.Getenv("TOTP_ENCRYPTION_KEY"))
//	ciphertext, _ := box.Seal([]byte("JBSWY3DPEHPK3PXP"))
//	plaintext, _ := box.Open(ciphertext)
//
// Key format: 64 hex chars (= 32 bytes = AES-256). The constructor
// rejects shorter keys explicitly so a misconfigured env doesn't
// silently degrade to a weak cipher.
//
// Ciphertext layout: nonce(12) || ciphertext_with_tag. Self-contained;
// no separate IV column needed on the table.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// SecretBox is the AES-256-GCM seal/open primitive.
type SecretBox struct {
	gcm cipher.AEAD
}

// NewSecretBox parses a 64-hex-char AES-256 key + returns a ready
// SecretBox. The key MUST come from a secrets manager / KMS in prod;
// hard-coded defaults are deliberately not supported.
func NewSecretBox(hexKey string) (*SecretBox, error) {
	if len(hexKey) != 64 {
		return nil, fmt.Errorf("crypto: key must be 64 hex chars (got %d)", len(hexKey))
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("crypto: key not hex: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm: %w", err)
	}
	return &SecretBox{gcm: gcm}, nil
}

// Seal encrypts plaintext + prepends a fresh random 12-byte nonce.
// Caller stores the result as-is (BYTEA in Postgres works fine).
func (b *SecretBox) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	// Seal appends to nonce so the result is nonce || ciphertext+tag
	// — single contiguous buffer for storage.
	return b.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal. Returns an error if the ciphertext was tampered
// with, the nonce is shorter than expected, or the key doesn't match.
func (b *SecretBox) Open(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < b.gcm.NonceSize() {
		return nil, errors.New("crypto: ciphertext shorter than nonce")
	}
	nonce, ct := ciphertext[:b.gcm.NonceSize()], ciphertext[b.gcm.NonceSize():]
	return b.gcm.Open(nil, nonce, ct, nil)
}
