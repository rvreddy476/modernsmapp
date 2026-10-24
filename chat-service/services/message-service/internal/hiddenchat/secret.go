// Package hiddenchat holds the secret handling for hidden chats (contract
// pinned 24 Oct 2026): the account's secret key (reveals the hidden list) and
// chat password (opens a hidden chat).
//
// Both secrets are stored only as Argon2id hashes in the PHC string format
// ($argon2id$v=19$m=..,t=..,p=..$salt$hash) with a fresh random salt per
// secret, and compared in constant time. Nothing in this package logs, and no
// error it returns carries the secret.
//
// Reveal and open session tokens are 32 random bytes handed to the client
// once; the server keeps only their SHA-256.
package hiddenchat

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: OWASP's 2024 minimum profile (19 MiB, t=2, p=1). The
// memory cost is per verification, so it is kept modest — every reveal/open
// attempt is also rate limited and locked out, which is the real defence
// against guessing a 4-character key.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 19 * 1024 // KiB
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	saltLen             = 16
)

// HashPrefix is what every stored hash starts with. The table carries a CHECK
// constraint on it, so a code path that tried to store a raw secret fails at
// the database.
const HashPrefix = "$argon2id$"

// Secret limits (contract).
const (
	SecretKeyMinLen    = 4
	SecretKeyMaxLen    = 12
	ChatPasswordMinLen = 6
	ChatPasswordMaxLen = 64
)

var (
	// ErrSecretKeyInvalid: not 4-12 letters/digits.
	ErrSecretKeyInvalid = errors.New("secret key must be 4-12 letters or digits")
	// ErrChatPasswordInvalid: not 6-64 characters.
	ErrChatPasswordInvalid = errors.New("chat password must be 6-64 characters")
	// ErrMalformedHash: a stored value that is not an Argon2id PHC string.
	ErrMalformedHash = errors.New("stored secret hash is malformed")
)

// ValidateSecretKey checks the secret key shape: 4-12 runes, each a letter
// or a digit (any script).
func ValidateSecretKey(key string) error {
	n := utf8.RuneCountInString(key)
	if n < SecretKeyMinLen || n > SecretKeyMaxLen || !utf8.ValidString(key) {
		return ErrSecretKeyInvalid
	}
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return ErrSecretKeyInvalid
		}
	}
	return nil
}

// ValidateChatPassword checks the chat password shape: 6-64 runes, valid
// UTF-8, no control characters.
func ValidateChatPassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < ChatPasswordMinLen || n > ChatPasswordMaxLen || !utf8.ValidString(pw) {
		return ErrChatPasswordInvalid
	}
	for _, r := range pw {
		if unicode.IsControl(r) {
			return ErrChatPasswordInvalid
		}
	}
	return nil
}

// Hash returns the Argon2id PHC string of secret with a fresh random salt.
func Hash(secret string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hidden chats: salt: %w", err)
	}
	return encode(salt, argonTime, argonMemory, argonThreads, argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)), nil
}

func encode(salt []byte, t, m uint32, p uint8, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s", HashPrefix, argon2.Version, m, t, p, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// Verify reports whether secret matches the stored PHC string. The final
// comparison is constant time. A malformed hash never matches.
func Verify(secret, stored string) bool {
	salt, t, m, p, want, err := decode(stored)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when there is nothing to verify (no secrets
// configured), so "not configured" costs the same time as a wrong key.
var dummyHash = func() string {
	h, err := Hash("hidden-chats-timing-equaliser")
	if err != nil {
		panic(err)
	}
	return h
}()

// VerifyOrDummy verifies against stored, or — when stored is empty — burns
// one verification on a dummy hash and returns false.
func VerifyOrDummy(secret, stored string) bool {
	if stored == "" {
		_ = Verify(secret, dummyHash)
		return false
	}
	return Verify(secret, stored)
}

func decode(stored string) (salt []byte, t, m uint32, p uint8, key []byte, err error) {
	if !strings.HasPrefix(stored, HashPrefix) {
		return nil, 0, 0, 0, nil, ErrMalformedHash
	}
	parts := strings.Split(strings.TrimPrefix(stored, HashPrefix), "$")
	// v=19, m=..,t=..,p=.., salt, key
	if len(parts) != 4 || parts[0] != "v="+strconv.Itoa(argon2.Version) {
		return nil, 0, 0, 0, nil, ErrMalformedHash
	}
	var mm, tt uint64
	var pp uint64
	for _, kv := range strings.Split(parts[1], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, 0, 0, 0, nil, ErrMalformedHash
		}
		n, perr := strconv.ParseUint(v, 10, 32)
		if perr != nil {
			return nil, 0, 0, 0, nil, ErrMalformedHash
		}
		switch k {
		case "m":
			mm = n
		case "t":
			tt = n
		case "p":
			pp = n
		default:
			return nil, 0, 0, 0, nil, ErrMalformedHash
		}
	}
	// Bounds: refuse parameters a tampered row could use to exhaust memory.
	if mm < 8*1024 || mm > 256*1024 || tt < 1 || tt > 10 || pp < 1 || pp > 8 {
		return nil, 0, 0, 0, nil, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err = b64.DecodeString(parts[2])
	if err != nil || len(salt) < 8 {
		return nil, 0, 0, 0, nil, ErrMalformedHash
	}
	key, err = b64.DecodeString(parts[3])
	if err != nil || len(key) < 16 {
		return nil, 0, 0, 0, nil, ErrMalformedHash
	}
	return salt, uint32(tt), uint32(mm), uint8(pp), key, nil
}

// TokenBytes is the size of a reveal / open token before encoding.
const TokenBytes = 32

// NewToken returns an opaque client token (base64url, 43 chars) and the
// SHA-256 the server stores.
func NewToken() (token string, hash []byte, err error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("hidden chats: token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, TokenHash(token), nil
}

// TokenHash is the stored form of a client token. A string that is not a
// well-formed token hashes to a value no issued token has, so lookups simply
// miss.
func TokenHash(token string) []byte {
	sum := sha256.Sum256([]byte("hidden-chat-token:" + token))
	return sum[:]
}

// WellFormedToken reports whether s could be a token this package issued
// (cheap pre-check before a database lookup).
func WellFormedToken(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(TokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil
}
