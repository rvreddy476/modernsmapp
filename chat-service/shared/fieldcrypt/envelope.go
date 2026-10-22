package fieldcrypt

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Wire-format sizes.
const (
	// DataKeySize is the only accepted data-key length (AES-256).
	DataKeySize = 32
	// NonceSize is the GCM nonce length.
	NonceSize = 12
	// HeaderSize is the version (4 bytes) plus the nonce.
	HeaderSize = 4 + NonceSize

	tagSize      = 16
	minSealedLen = HeaderSize + tagSize
	maxScopeLen  = 64
)

var (
	ErrNoKeyRing          = errors.New("fieldcrypt: no key ring configured")
	ErrInvalidScope       = errors.New("fieldcrypt: scope must be 1-64 characters of a-z, 0-9, '_', '.', '-'")
	ErrScopeNotConfigured = errors.New("fieldcrypt: no active key for scope")
	ErrUnknownKeyVersion  = errors.New("fieldcrypt: key version is not in the key ring")
	ErrInvalidKeyVersion  = errors.New("fieldcrypt: invalid key version")
	ErrBadDataKey         = errors.New("fieldcrypt: data key must be 32 non-zero bytes")
	ErrBadCiphertext      = errors.New("fieldcrypt: ciphertext is malformed or does not authenticate")
	ErrEmptyPlaintext     = errors.New("fieldcrypt: refusing to seal an empty value")
)

// Scope separates key material and binds ciphertext to its column or retention
// class. It is 1-64 characters of [a-z0-9_.-], for example "profile",
// "order_snapshot" or "food.payout_account".
type Scope string

// Validate reports whether s is a well-formed scope.
func (s Scope) Validate() error {
	if len(s) == 0 || len(s) > maxScopeLen {
		return ErrInvalidScope
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '.', c == '-':
		default:
			return ErrInvalidScope
		}
	}
	return nil
}

// KeyError describes a key-resolution failure. Its message names the scope and
// version only; it never contains key bytes or plaintext. Unwrap returns the
// underlying sentinel or ring error.
type KeyError struct {
	Scope   Scope
	Version uint32
	Err     error
}

func (e *KeyError) Error() string {
	var b strings.Builder
	b.WriteString("fieldcrypt: ")
	if e.Scope.Validate() == nil {
		fmt.Fprintf(&b, "scope %q", string(e.Scope))
	} else {
		b.WriteString("invalid scope")
	}
	if e.Version != 0 {
		fmt.Fprintf(&b, " key version %d", e.Version)
	}
	b.WriteString(": ")
	if e.Err != nil {
		b.WriteString(strings.TrimPrefix(e.Err.Error(), "fieldcrypt: "))
	} else {
		b.WriteString("key error")
	}
	return b.String()
}

func (e *KeyError) Unwrap() error { return e.Err }

// Sealer seals and opens values for exactly one scope. It caches nothing: every
// Seal asks the ring for the active version, so a rotation takes effect at once.
// Caching plaintext keys is the ring's job.
type Sealer struct {
	ring  KeyRing
	scope Scope
}

// NewSealer builds a Sealer and fails closed: the scope must be valid, the ring
// non-nil, and the ring must already hold a 32-byte active key for the scope.
// Every failure is a *KeyError.
func NewSealer(ctx context.Context, ring KeyRing, scope Scope) (*Sealer, error) {
	if ring == nil {
		return nil, &KeyError{Scope: scope, Err: ErrNoKeyRing}
	}
	if err := scope.Validate(); err != nil {
		return nil, &KeyError{Scope: scope, Err: err}
	}
	s := &Sealer{ring: ring, scope: scope}
	if _, _, err := s.activeKey(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Scope returns the scope this sealer is bound to.
func (s *Sealer) Scope() Scope { return s.scope }

// Seal encrypts a non-empty value under the scope's active key and returns the
// sealed blob and the key version it used. The additional data is the scope
// alone, exactly as in Architecture/shared/pii.
func (s *Sealer) Seal(ctx context.Context, plaintext string) ([]byte, uint32, error) {
	return s.SealBound(ctx, plaintext, nil)
}

// SealBound is Seal with the ciphertext also bound to a row: the additional
// data is the scope, a 0x00 separator and the binding (see Bind). A blob
// copied into a row with a different binding fails to open. A nil or empty
// binding is exactly Seal, so the layout stays byte-compatible with pii.
func (s *Sealer) SealBound(ctx context.Context, plaintext string, binding []byte) ([]byte, uint32, error) {
	if plaintext == "" {
		return nil, 0, ErrEmptyPlaintext
	}
	key, version, err := s.activeKey(ctx)
	if err != nil {
		return nil, 0, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, 0, &KeyError{Scope: s.scope, Version: version, Err: ErrBadDataKey}
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, errors.New("fieldcrypt: could not read a random nonce")
	}
	out := make([]byte, HeaderSize, HeaderSize+len(plaintext)+tagSize)
	binary.BigEndian.PutUint32(out[:4], version)
	copy(out[4:HeaderSize], nonce)
	out = aead.Seal(out, nonce, []byte(plaintext), additionalData(s.scope, binding))
	return out, version, nil
}

// Open decrypts a blob produced by Seal (or by commerce-service/internal/pii for
// the same scope). The key version comes from the blob's header, never from the
// ring's active version. Any authentication failure is ErrBadCiphertext, which
// does not reveal whether the cause was tampering, a wrong scope or a wrong key.
func (s *Sealer) Open(ctx context.Context, sealed []byte) (string, error) {
	return s.OpenBound(ctx, sealed, nil)
}

// OpenBound opens a blob produced by SealBound with the same binding. A wrong
// binding is ErrBadCiphertext, like any other authentication failure.
func (s *Sealer) OpenBound(ctx context.Context, sealed []byte, binding []byte) (string, error) {
	if len(sealed) < minSealedLen {
		return "", ErrBadCiphertext
	}
	version := binary.BigEndian.Uint32(sealed[:4])
	if version == 0 {
		return "", ErrBadCiphertext
	}
	key, err := s.dataKey(ctx, version)
	if err != nil {
		return "", err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return "", &KeyError{Scope: s.scope, Version: version, Err: ErrBadDataKey}
	}
	pt, err := aead.Open(nil, sealed[4:HeaderSize], sealed[HeaderSize:], additionalData(s.scope, binding))
	if err != nil {
		return "", ErrBadCiphertext
	}
	return string(pt), nil
}

// KeyVersion reads the key version from a sealed blob without decrypting it,
// for re-seal backfills after a rotation.
func KeyVersion(sealed []byte) (uint32, error) {
	if len(sealed) < minSealedLen {
		return 0, ErrBadCiphertext
	}
	version := binary.BigEndian.Uint32(sealed[:4])
	if version == 0 {
		return 0, ErrBadCiphertext
	}
	return version, nil
}

func (s *Sealer) activeKey(ctx context.Context) ([]byte, uint32, error) {
	version, err := s.ring.ActiveVersion(ctx, s.scope)
	if err != nil {
		return nil, 0, &KeyError{Scope: s.scope, Err: err}
	}
	if version == 0 {
		return nil, 0, &KeyError{Scope: s.scope, Err: ErrScopeNotConfigured}
	}
	key, err := s.dataKey(ctx, version)
	if err != nil {
		return nil, 0, err
	}
	return key, version, nil
}

// dataKey resolves an exact version and re-checks the key length on every call:
// aes.NewCipher would otherwise accept a 16- or 24-byte key as AES-128/192.
func (s *Sealer) dataKey(ctx context.Context, version uint32) ([]byte, error) {
	key, err := s.ring.DataKey(ctx, s.scope, version)
	if err != nil {
		return nil, &KeyError{Scope: s.scope, Version: version, Err: err}
	}
	if len(key) != DataKeySize {
		return nil, &KeyError{Scope: s.scope, Version: version, Err: ErrBadDataKey}
	}
	return key, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// additionalData is the GCM additional data: the scope alone when unbound
// (byte-compatible with pii), otherwise scope || 0x00 || binding. A scope
// never contains 0x00, so the split is unambiguous.
func additionalData(scope Scope, binding []byte) []byte {
	if len(binding) == 0 {
		return []byte(scope)
	}
	ad := make([]byte, 0, len(scope)+1+len(binding))
	ad = append(ad, scope...)
	ad = append(ad, 0x00)
	return append(ad, binding...)
}

// Bind encodes the parts that identify a row (conversation id, message id,
// ...) as the binding for SealBound and OpenBound. Each part is length-
// prefixed, so ("ab","c") and ("a","bc") never collide.
func Bind(parts ...string) []byte {
	n := 0
	for _, p := range parts {
		n += binary.MaxVarintLen64 + len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = binary.AppendUvarint(out, uint64(len(p)))
		out = append(out, p...)
	}
	return out
}
