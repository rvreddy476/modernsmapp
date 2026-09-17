// Package servicetoken verifies the short-lived, audience-scoped Ed25519
// tokens admin-service signs when it calls identity for a human admin.
//
// WHY A LOCAL COPY. The canonical implementation is
// Architecture/shared/servicetoken. Identity's Docker build context is
// identity-platform/ alone (Dockerfile: COPY shared/ and services/auth-service/,
// GOWORK=off), so that module cannot be imported here. This package is the
// VERIFY half of it, wire-compatible on purpose: same JOSE header
// {"alg":"EdDSA","typ":"JWT","kid"}, same claim names (iss, sub, aud, exp,
// nbf, iat, jti, scope, ref_types, act), same base64url framing and the same
// signing input (header.claims). fixture_shared_test.go holds a token minted
// by the shared package; it must verify here, byte for byte.
//
// What it deliberately does NOT have: a production signer (identity never
// mints service tokens — tokentest holds a test-only one), reference types
// (identity's admin operations are not per-reference) and a caller list wider
// than admin-service.
//
// The algorithm is pinned. The header's alg is required to be EdDSA but never
// used to choose a verifier, so "none" or an HMAC forged with the public key
// as its secret cannot steer verification.
package servicetoken

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Algorithm is the only accepted signature algorithm.
const Algorithm = "EdDSA"

// MaxTTL mirrors the shared package: a correctly signed token that lives
// longer than this (plus a minute of skew) is refused, because a short window
// is the only control against replay of a stolen token.
const MaxTTL = 5 * time.Minute

// Errors. Names match the shared package so a reader of either sees the same
// vocabulary.
var (
	ErrBadAlgorithm  = errors.New("servicetoken: unexpected algorithm")
	ErrMalformed     = errors.New("servicetoken: malformed token")
	ErrUnknownKID    = errors.New("servicetoken: unknown key id")
	ErrBadSignature  = errors.New("servicetoken: signature verification failed")
	ErrExpired       = errors.New("servicetoken: token expired")
	ErrNotYetValid   = errors.New("servicetoken: token not yet valid")
	ErrTTLTooLong    = errors.New("servicetoken: ttl exceeds the permitted maximum")
	ErrWrongAudience = errors.New("servicetoken: wrong audience")
	ErrUnknownIssuer = errors.New("servicetoken: unknown issuer")
	ErrScopeDenied   = errors.New("servicetoken: operation not in scope")
	ErrNoJTI         = errors.New("servicetoken: token has no jti")
)

// Header is the JOSE header.
type Header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	KID string `json:"kid"`
}

// Claims is the token body, in the shared package's field order.
type Claims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  string   `json:"aud"`
	Expiry    int64    `json:"exp"`
	NotBefore int64    `json:"nbf"`
	IssuedAt  int64    `json:"iat"`
	JTI       string   `json:"jti"`
	Scope     []string `json:"scope"`
	RefTypes  []string `json:"ref_types"`
	// Actor is the human user the calling service acts for. Signed, so it can
	// be trusted for attribution; a header cannot.
	Actor string `json:"act,omitempty"`
}

// CallerPolicy is what one issuer may ask for.
type CallerPolicy struct {
	PublicKey ed25519.PublicKey
	// Operations this caller may request; a token whose scope names an
	// operation outside this list is refused for that operation.
	Operations []string
}

// Verifier validates tokens for one audience.
type Verifier struct {
	audience string
	callers  map[string]CallerPolicy // "<issuer>/<kid>"
	now      func() time.Time
}

// NewVerifier builds a verifier for the given audience.
func NewVerifier(audience string) *Verifier {
	return &Verifier{audience: audience, callers: map[string]CallerPolicy{}, now: time.Now}
}

// Register adds or replaces a caller policy.
func (v *Verifier) Register(issuer, kid string, p CallerPolicy) error {
	if issuer == "" || kid == "" {
		return fmt.Errorf("servicetoken: issuer and kid are required")
	}
	if len(p.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("servicetoken: public key for %s/%s must be %d bytes", issuer, kid, ed25519.PublicKeySize)
	}
	v.callers[issuer+"/"+kid] = p
	return nil
}

// RegisterBase64 is the deployment-friendly form of Register: the public key
// as base64 (std or raw, padded or not).
func (v *Verifier) RegisterBase64(issuer, kid, pubB64 string, ops []string) error {
	raw, err := DecodeBase64(pubB64)
	if err != nil {
		return fmt.Errorf("servicetoken: decode public key for %s/%s: %w", issuer, kid, err)
	}
	return v.Register(issuer, kid, CallerPolicy{PublicKey: ed25519.PublicKey(raw), Operations: ops})
}

// Callers reports how many caller keys are registered.
func (v *Verifier) Callers() int { return len(v.callers) }

// SetClock is for tests only.
func (v *Verifier) SetClock(f func() time.Time) { v.now = f }

// Verified is the result of a successful verification.
type Verified struct {
	Issuer  string
	Subject string
	Scope   []string
	JTI     string
	// Actor is the signed "act" claim; empty when the caller acts for no one.
	Actor string
	// ExpiresAt is when the token stops being valid; a replay guard keys its
	// record on JTI for exactly this long.
	ExpiresAt time.Time
}

// Verify checks a token and authorizes one operation ("" skips the scope
// check). Order matters, exactly as in the shared package: the key is resolved
// from the REGISTERED policy by (iss, kid), the signature is checked, and only
// then are the temporal and authorization claims read — an unverified token's
// contents never make a decision.
func (v *Verifier) Verify(token, operation string) (*Verified, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	var hdr Header
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return nil, ErrMalformed
	}
	if hdr.Alg != Algorithm {
		return nil, ErrBadAlgorithm
	}
	if hdr.KID == "" {
		return nil, ErrUnknownKID
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	var claims Claims
	if err := json.Unmarshal(cb, &claims); err != nil {
		return nil, ErrMalformed
	}
	if claims.Issuer == "" {
		return nil, ErrUnknownIssuer
	}
	policy, ok := v.callers[claims.Issuer+"/"+hdr.KID]
	if !ok {
		return nil, ErrUnknownIssuer
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	if !ed25519.Verify(policy.PublicKey, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, ErrBadSignature
	}

	now := v.now()
	if claims.Expiry == 0 || now.Unix() >= claims.Expiry {
		return nil, ErrExpired
	}
	if claims.NotBefore != 0 && now.Unix() < claims.NotBefore {
		return nil, ErrNotYetValid
	}
	if claims.Expiry-claims.IssuedAt > int64(MaxTTL.Seconds())+60 {
		return nil, ErrTTLTooLong
	}
	if subtle.ConstantTimeCompare([]byte(claims.Audience), []byte(v.audience)) != 1 {
		return nil, ErrWrongAudience
	}
	if strings.TrimSpace(claims.JTI) == "" {
		// Identity records the jti in every audit row it writes for a token
		// caller and uses it to refuse a replayed token; a token without one
		// cannot be attributed or de-duplicated.
		return nil, ErrNoJTI
	}
	if operation != "" && (!contains(claims.Scope, operation) || !contains(policy.Operations, operation)) {
		return nil, ErrScopeDenied
	}
	return &Verified{
		Issuer:    claims.Issuer,
		Subject:   claims.Subject,
		Scope:     claims.Scope,
		JTI:       claims.JTI,
		Actor:     claims.Actor,
		ExpiresAt: time.Unix(claims.Expiry, 0),
	}, nil
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// DecodeBase64 accepts std or URL alphabets, padded or not — the shapes a
// key arrives in from a secret store.
func DecodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not valid base64")
}
