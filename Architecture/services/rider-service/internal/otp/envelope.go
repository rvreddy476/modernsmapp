package otp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

var (
	// ErrDecryptionFailed is returned when OTP decryption fails.
	ErrDecryptionFailed = errors.New("otp: decryption failed")
	// ErrInvalidCiphertext is returned when ciphertext is malformed.
	ErrInvalidCiphertext = errors.New("otp: invalid ciphertext")
)

// EncryptOTP encrypts the plaintext OTP with AES-GCM-256 using an envelope master key.
func EncryptOTP(plaintext string, masterKey []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("otp: empty plaintext")
	}
	if len(masterKey) == 0 {
		var err error
		masterKey, err = getMasterKey()
		if err != nil {
			return nil, err
		}
	}
	key := sha256.Sum256(masterKey)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertext, nil
}

// DecryptOTP decrypts the encrypted OTP using the envelope master key.
func DecryptOTP(ciphertext []byte, masterKey []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", ErrInvalidCiphertext
	}
	if len(masterKey) == 0 {
		var err error
		masterKey, err = getMasterKey()
		if err != nil {
			return "", err
		}
	}
	key := sha256.Sum256(masterKey)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", ErrInvalidCiphertext
	}
	nonce := ciphertext[:gcm.NonceSize()]
	actualCiphertext := ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return "", ErrDecryptionFailed
	}
	return string(plaintext), nil
}

func getMasterKey() ([]byte, error) {
	k := os.Getenv("MOPEDU_OTP_ENCRYPTION_KEY")
	if k == "" {
		k = os.Getenv("JWT_SECRET")
	}
	if k == "" {
		if isProductionEnv() {
			return nil, errors.New("otp: MOPEDU_OTP_ENCRYPTION_KEY required in production")
		}
		k = "mopedu-default-envelope-key-dev-only"
	}
	return []byte(k), nil
}

func isProductionEnv() bool {
	for _, env := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		v := os.Getenv(env)
		if v == "production" || v == "prod" || v == "staging" {
			return true
		}
	}
	return false
}
