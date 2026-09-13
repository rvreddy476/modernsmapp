package pii

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinLookupSaltSize is the shortest accepted lookup salt.
const MinLookupSaltSize = 16

var (
	ErrSaltTooShort       = errors.New("pii: lookup salt must be at least 16 bytes")
	ErrNoNormaliser       = errors.New("pii: lookup hasher needs a normaliser")
	ErrInvalidDomain      = errors.New("pii: lookup domain must be non-empty and free of NUL")
	ErrEmptyLookupInput   = errors.New("pii: lookup value is empty after normalisation")
	ErrInvalidLookupInput = errors.New("pii: lookup value is not valid UTF-8")
)

// Normaliser canonicalises an identifier before hashing. It has the same shape
// as shared/kyc NormalizeIFSC and NormalizeBankAccountNumber. Its errors must
// not echo the input.
type Normaliser func(string) (string, error)

// CompactUpper removes whitespace, '-', '.' and '/' and upper-cases the rest.
// It suits vehicle registration numbers, driving-licence numbers and PAN.
func CompactUpper(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrInvalidLookupInput
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isCompactSeparator(r) {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String(), nil
}

func isCompactSeparator(r rune) bool {
	return unicode.IsSpace(r) || r == '-' || r == '.' || r == '/'
}

// LookupHasher derives HMAC-SHA256(salt, domain || 0x00 || normalised value),
// encoded as unpadded base64url (43 characters). The domain names the kind of
// identifier ("pan", "bank_account", "vehicle_registration",
// "driving_licence"), so equal strings of different kinds never correlate,
// while the same kind in different tables still can.
//
// A hash is for exact-match lookup only. It is never displayed or logged, and
// it never replaces sealing: identifiers are low-entropy and brute-forceable by
// anyone holding the salt.
type LookupHasher struct {
	salt      []byte
	domain    string
	normalise Normaliser
}

// NewLookupHasher builds a hasher. The salt (at least 16 bytes) is copied.
func NewLookupHasher(salt []byte, domain string, normalise Normaliser) (*LookupHasher, error) {
	if len(salt) < MinLookupSaltSize {
		return nil, ErrSaltTooShort
	}
	if domain == "" || strings.IndexByte(domain, 0) >= 0 {
		return nil, ErrInvalidDomain
	}
	if normalise == nil {
		return nil, ErrNoNormaliser
	}
	return &LookupHasher{
		salt:      append([]byte(nil), salt...),
		domain:    domain,
		normalise: normalise,
	}, nil
}

// Hash normalises value and returns its lookup hash. A value that is empty after
// normalisation is refused, because every blank would otherwise collide.
func (h *LookupHasher) Hash(value string) (string, error) {
	normalised, err := h.normalise(value)
	if err != nil {
		return "", fmt.Errorf("pii: lookup normalise: %w", err)
	}
	if normalised == "" {
		return "", ErrEmptyLookupInput
	}
	mac := hmac.New(sha256.New, h.salt)
	mac.Write([]byte(h.domain))
	mac.Write([]byte{0})
	mac.Write([]byte(normalised))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
