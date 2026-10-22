package fieldcrypt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
)

// Prefix marks a sealed column value: "cfe1:" (chat field envelope, format 1)
// followed by the sealed blob in unpadded base64url. It is plain ASCII, so it
// fits a TEXT column as is and a JSONB column as a JSON string without any
// escaping.
const Prefix = "cfe1:"

var (
	// ErrPlaintextMode is returned when a sealed value is read by a Cipher
	// that has no key ring: the value cannot be opened, and returning the
	// stored text would hand ciphertext to a reader.
	ErrPlaintextMode = errors.New("fieldcrypt: a sealed value cannot be opened without a key ring")
)

// Cipher is the column codec the stores use. Seal* writes a sealed value;
// Open* reads one back and passes a legacy value (written before encryption,
// so without Prefix) through unchanged. That dual read is what lets a
// deployment switch encryption on with rows of both kinds in the table.
//
// A nil *Cipher and Plaintext() write plaintext and can only read plaintext.
// Methods are safe for concurrent use.
type Cipher struct {
	ring    KeyRing
	sealers sync.Map // Scope -> *Sealer
}

// New builds a Cipher over a ring. It fails closed on a nil ring.
func New(ring KeyRing) (*Cipher, error) {
	if ring == nil {
		return nil, &KeyError{Err: ErrNoKeyRing}
	}
	return &Cipher{ring: ring}, nil
}

// Plaintext is the development Cipher: writes are stored as given. Services
// must refuse it in production.
func Plaintext() *Cipher { return &Cipher{} }

// Enabled reports whether writes are sealed.
func (c *Cipher) Enabled() bool { return c != nil && c.ring != nil }

func (c *Cipher) sealer(ctx context.Context, scope Scope) (*Sealer, error) {
	if v, ok := c.sealers.Load(scope); ok {
		return v.(*Sealer), nil
	}
	s, err := NewSealer(ctx, c.ring, scope)
	if err != nil {
		return nil, err
	}
	actual, _ := c.sealers.LoadOrStore(scope, s)
	return actual.(*Sealer), nil
}

// SealString seals a column value bound to its row. An empty value stays
// empty: "" carries no content and several columns use it as "none".
func (c *Cipher) SealString(ctx context.Context, scope Scope, binding []byte, plaintext string) (string, error) {
	if plaintext == "" || !c.Enabled() {
		return plaintext, nil
	}
	s, err := c.sealer(ctx, scope)
	if err != nil {
		return "", err
	}
	blob, _, err := s.SealBound(ctx, plaintext, binding)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(Prefix)+base64.RawURLEncoding.EncodedLen(len(blob)))
	copy(out, Prefix)
	base64.RawURLEncoding.Encode(out[len(Prefix):], blob)
	return string(out), nil
}

// OpenString reads a column value. A legacy (unsealed) value is returned as
// stored; a sealed one must open under this scope and binding.
func (c *Cipher) OpenString(ctx context.Context, scope Scope, binding []byte, stored string) (string, error) {
	blob, sealed := decodeSealed(stored)
	if !sealed {
		return stored, nil
	}
	if !c.Enabled() {
		return "", ErrPlaintextMode
	}
	s, err := c.sealer(ctx, scope)
	if err != nil {
		return "", err
	}
	return s.OpenBound(ctx, blob, binding)
}

// SealJSON seals a JSON document for a JSONB (or JSON-in-TEXT) column. The
// result is a JSON string literal holding the sealed value, so the column
// stays valid JSON. Nil or empty input stays as is (NULL / no payload).
func (c *Cipher) SealJSON(ctx context.Context, scope Scope, binding []byte, doc []byte) ([]byte, error) {
	if len(doc) == 0 || !c.Enabled() {
		return doc, nil
	}
	sealed, err := c.SealString(ctx, scope, binding, string(doc))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(sealed)+2)
	out = append(out, '"')
	out = append(out, sealed...)
	return append(out, '"'), nil
}

// OpenJSON reads a JSON column written by SealJSON, or a legacy document,
// which is returned as stored.
func (c *Cipher) OpenJSON(ctx context.Context, scope Scope, binding []byte, stored []byte) ([]byte, error) {
	inner, ok := sealedJSONString(stored)
	if !ok {
		return stored, nil
	}
	pt, err := c.OpenString(ctx, scope, binding, inner)
	if err != nil {
		return nil, err
	}
	return []byte(pt), nil
}

// IsSealed reports whether a stored column value is a sealed value. It is a
// structural check (prefix, base64url, minimum length, non-zero version) and
// decrypts nothing.
func IsSealed(stored string) bool {
	_, ok := decodeSealed(stored)
	return ok
}

// IsSealedJSON is IsSealed for a JSON column value.
func IsSealedJSON(stored []byte) bool {
	_, ok := sealedJSONString(stored)
	return ok
}

// SealedKeyVersion reads the key version of a sealed column value, for
// re-seal backfills after a rotation.
func SealedKeyVersion(stored string) (uint32, bool) {
	blob, ok := decodeSealed(stored)
	if !ok {
		return 0, false
	}
	return binary.BigEndian.Uint32(blob[:4]), true
}

func decodeSealed(stored string) ([]byte, bool) {
	if !strings.HasPrefix(stored, Prefix) {
		return nil, false
	}
	enc := stored[len(Prefix):]
	if base64.RawURLEncoding.DecodedLen(len(enc)) < minSealedLen {
		return nil, false
	}
	blob, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || len(blob) < minSealedLen || binary.BigEndian.Uint32(blob[:4]) == 0 {
		return nil, false
	}
	return blob, true
}

func sealedJSONString(stored []byte) (string, bool) {
	b := bytes.TrimSpace(stored)
	if len(b) < len(Prefix)+2 || b[0] != '"' || b[len(b)-1] != '"' {
		return "", false
	}
	inner := string(b[1 : len(b)-1])
	if !IsSealed(inner) {
		return "", false
	}
	return inner, true
}
