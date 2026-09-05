// Package servicetoken mints and verifies short-lived, audience-scoped
// tokens for service-to-service calls.
//
// Amendment A2. Before this package the only thing standing between an end
// user and payments-service was `X-Internal-Service-Key`, a single
// cluster-wide secret that the API gateway injected into EVERY proxied
// request. Any authenticated user therefore called payments with full
// service authority. The fix is not a better shared secret: a shared secret
// that every verifier holds is a secret every verifier can also MINT with.
//
// So this is asymmetric. Each calling service holds its own Ed25519 private
// key and signs its own tokens; payments holds only public keys and can
// verify but never forge. Two callers cannot impersonate each other because
// they have different keys, and a leaked verifier gains nothing.
//
// Ed25519 rather than RS256: no key-size or padding decisions, no way to
// configure it weakly, and it is in the standard library, so this adds no
// dependency to a module graph that deliberately hand-rolls its JWTs
// (see api-gateway/pkg/tokenpolicy).
//
// The algorithm is pinned at both ends. A verifier that reads `alg` from the
// header it is verifying will accept `none`, or an HMAC forged with the
// public key as its secret. This one never looks at the header to decide
// what to do; it decides first and then requires the header to match.
package servicetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Header is the JOSE header. `alg` is always EdDSA; anything else is refused
// before a signature is even computed.
type Header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	KID string `json:"kid"`
}

// Claims is the service-token body.
//
// Every field here is load-bearing for an authorization decision. `Scope`
// and `RefTypes` are what stop food-service's token from refunding a
// commerce order: a bare "this is a trusted service" assertion is not enough
// when two different domains share one payments-service.
type Claims struct {
	Issuer   string   `json:"iss"`
	Subject  string   `json:"sub"`
	Audience string   `json:"aud"`
	Expiry   int64    `json:"exp"`
	NotBefore int64   `json:"nbf"`
	IssuedAt int64    `json:"iat"`
	JTI      string   `json:"jti"`
	Scope    []string `json:"scope"`
	RefTypes []string `json:"ref_types"`
}

// Algorithm is the only accepted signature algorithm.
const Algorithm = "EdDSA"

// MaxTTL bounds how long a service token may live. A2 requires a short TTL:
// the residual risk after this package is a stolen token replayed inside its
// own validity window, and the only lever against that is to make the window
// small. Five minutes is ample for an in-cluster RPC.
const MaxTTL = 5 * time.Minute

var (
	ErrBadAlgorithm   = errors.New("servicetoken: unexpected algorithm")
	ErrMalformed      = errors.New("servicetoken: malformed token")
	ErrUnknownKID     = errors.New("servicetoken: unknown key id")
	ErrBadSignature   = errors.New("servicetoken: signature verification failed")
	ErrExpired        = errors.New("servicetoken: token expired")
	ErrNotYetValid    = errors.New("servicetoken: token not yet valid")
	ErrTTLTooLong     = errors.New("servicetoken: ttl exceeds the permitted maximum")
	ErrWrongAudience  = errors.New("servicetoken: wrong audience")
	ErrUnknownIssuer  = errors.New("servicetoken: unknown issuer")
	ErrScopeDenied    = errors.New("servicetoken: operation not in scope")
	ErrRefTypeDenied  = errors.New("servicetoken: reference type not permitted for this caller")
	ErrUserTokenGiven = errors.New("servicetoken: a user token was presented on a service-only route")
)

// Signer mints tokens for one calling service.
type Signer struct {
	issuer string
	kid    string
	key    ed25519.PrivateKey
}

// NewSigner builds a signer from a seed-or-full Ed25519 private key.
func NewSigner(issuer, kid string, priv ed25519.PrivateKey) (*Signer, error) {
	if issuer == "" || kid == "" {
		return nil, fmt.Errorf("servicetoken: issuer and kid are required")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("servicetoken: private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(priv))
	}
	return &Signer{issuer: issuer, kid: kid, key: priv}, nil
}

// NewSignerFromBase64 loads a base64 (std or raw, padded or not) private key,
// accepting either a 32-byte seed or a 64-byte expanded key. Deployments hand
// the key over as a secret string, so this is the shape that matters.
func NewSignerFromBase64(issuer, kid, b64 string) (*Signer, error) {
	raw, err := decodeB64(b64)
	if err != nil {
		return nil, fmt.Errorf("servicetoken: decode private key: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return NewSigner(issuer, kid, ed25519.NewKeyFromSeed(raw))
	case ed25519.PrivateKeySize:
		return NewSigner(issuer, kid, ed25519.PrivateKey(raw))
	default:
		return nil, fmt.Errorf("servicetoken: private key must be %d or %d bytes, got %d",
			ed25519.SeedSize, ed25519.PrivateKeySize, len(raw))
	}
}

// Mint issues a token for one operation against one reference type.
//
// Scope and reference type are per-token, not per-key, so a call that only
// needs to create an intent cannot be replayed to issue a refund.
func (s *Signer) Mint(audience, subject string, scope []string, refTypes []string, ttl time.Duration) (string, error) {
	if audience == "" {
		return "", fmt.Errorf("servicetoken: audience is required")
	}
	if ttl <= 0 || ttl > MaxTTL {
		return "", ErrTTLTooLong
	}
	now := time.Now()
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("servicetoken: jti: %w", err)
	}
	claims := Claims{
		Issuer:    s.issuer,
		Subject:   subject,
		Audience:  audience,
		Expiry:    now.Add(ttl).Unix(),
		NotBefore: now.Add(-30 * time.Second).Unix(), // small clock-skew allowance
		IssuedAt:  now.Unix(),
		JTI:       base64.RawURLEncoding.EncodeToString(jti),
		Scope:     scope,
		RefTypes:  refTypes,
	}
	hb, err := json.Marshal(Header{Alg: Algorithm, Typ: "JWT", KID: s.kid})
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sig := ed25519.Sign(s.key, []byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// CallerPolicy is what one issuer is permitted to do.
//
// The allowlist is the point. payments-service is shared between commerce
// and food; without a per-caller reference-type restriction, food's
// legitimate token would be able to refund a commerce order, and the
// separation of keys would buy nothing.
type CallerPolicy struct {
	// PublicKey verifies this caller's tokens.
	PublicKey ed25519.PublicKey
	// Operations this caller may request, e.g. {"payments:intent.create"}.
	Operations []string
	// RefTypes this caller may act on, e.g. {"order"} for commerce and
	// {"food_order"} for food.
	RefTypes []string
}

// Verifier validates tokens for one audience.
type Verifier struct {
	audience string
	// callers is keyed by "<issuer>/<kid>" so one issuer can rotate keys
	// while both remain acceptable.
	callers map[string]CallerPolicy
	now     func() time.Time
}

// NewVerifier builds a verifier for the given audience.
func NewVerifier(audience string) *Verifier {
	return &Verifier{
		audience: audience,
		callers:  map[string]CallerPolicy{},
		now:      time.Now,
	}
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

// RegisterBase64 is the deployment-friendly form of Register.
func (v *Verifier) RegisterBase64(issuer, kid, pubB64 string, ops, refTypes []string) error {
	raw, err := decodeB64(pubB64)
	if err != nil {
		return fmt.Errorf("servicetoken: decode public key for %s/%s: %w", issuer, kid, err)
	}
	return v.Register(issuer, kid, CallerPolicy{
		PublicKey:  ed25519.PublicKey(raw),
		Operations: ops,
		RefTypes:   refTypes,
	})
}

// Callers reports how many caller keys are registered. Startup validation
// uses it to refuse to boot with an empty allowlist.
func (v *Verifier) Callers() int { return len(v.callers) }

// SetClock is for tests only.
func (v *Verifier) SetClock(f func() time.Time) { v.now = f }

// Verified is the result of a successful verification.
type Verified struct {
	Issuer   string
	Subject  string
	Scope    []string
	RefTypes []string
	JTI      string
}

// Verify checks a token and authorizes one (operation, referenceType) pair.
//
// Order matters. The algorithm and key are resolved from the REGISTERED
// policy, not from the token, so a forged header cannot steer verification.
// Only then is the signature checked, and only after that are the temporal
// and authorization claims read — an unverified token's contents are never
// used to make a decision.
func (v *Verifier) Verify(token, operation, refType string) (*Verified, error) {
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
		// Explicitly refuse "none", HS256-with-public-key, and anything else.
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
		// A long-lived token defeats the only control we have against
		// replay, so refuse it even though it is correctly signed.
		return nil, ErrTTLTooLong
	}
	if subtle.ConstantTimeCompare([]byte(claims.Audience), []byte(v.audience)) != 1 {
		return nil, ErrWrongAudience
	}
	if operation != "" {
		if !contains(claims.Scope, operation) || !contains(policy.Operations, operation) {
			return nil, ErrScopeDenied
		}
	}
	if refType != "" {
		if !contains(claims.RefTypes, refType) || !contains(policy.RefTypes, refType) {
			return nil, ErrRefTypeDenied
		}
	}
	return &Verified{
		Issuer:   claims.Issuer,
		Subject:  claims.Subject,
		Scope:    claims.Scope,
		RefTypes: claims.RefTypes,
		JTI:      claims.JTI,
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

func decodeB64(s string) ([]byte, error) {
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

// GenerateKeypair is a helper for local development and tests. Production
// keys are generated out of band and delivered through External Secrets.
func GenerateKeypair() (pubB64, privB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv), nil
}

// Operation names. Kept as constants so a typo is a compile error rather
// than a silently denied — or silently granted — call.
const (
	OpIntentCreate = "payments:intent.create"
	OpIntentRead   = "payments:intent.read"
	OpRefundCreate = "payments:refund.create"
	OpPaymentFetch = "payments:payment.fetch"
)

// Reference types. `order` belongs to commerce; `food_order` to food.
const (
	RefOrder     = "order"
	RefFoodOrder = "food_order"
)

// AudiencePayments is the audience string payments-service accepts.
const AudiencePayments = "payments"
