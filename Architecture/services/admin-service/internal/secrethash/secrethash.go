// Package secrethash hashes and verifies long-lived client secrets with
// argon2id.
//
// bcrypt is not in the Architecture vendor tree; argon2 is. The encoded form is
// the PHC string format, so the parameters travel with every hash and can be
// raised later without breaking stored rows:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<base64 salt>$<base64 key>
package secrethash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	prefix     = "$argon2id$"
	memory     = 64 * 1024
	iterations = 3
	threads    = 2
	saltLen    = 16
	keyLen     = 32
)

var (
	ErrEmptySecret = errors.New("secrethash: empty secret")
	ErrInvalidHash = errors.New("secrethash: invalid hash format")
	ErrMismatch    = errors.New("secrethash: secret does not match hash")
)

// Hash returns the encoded argon2id hash of secret with a fresh random salt.
func Hash(secret string) (string, error) {
	if secret == "" {
		return "", ErrEmptySecret
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(secret), salt, iterations, memory, threads, keyLen)
	return fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s", prefix, argon2.Version, memory, iterations, threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// IsHash reports whether stored looks like a hash this package produced, as
// opposed to a legacy plaintext value.
func IsHash(stored string) bool {
	return strings.HasPrefix(stored, prefix)
}

// Verify returns nil when secret matches encoded, ErrMismatch when it does not,
// and ErrInvalidHash when encoded is not a parsable argon2id hash. The key
// comparison is constant-time.
func Verify(encoded, secret string) error {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return ErrInvalidHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return ErrInvalidHash
	}
	got := argon2.IDKey([]byte(secret), salt, t, m, p, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}
