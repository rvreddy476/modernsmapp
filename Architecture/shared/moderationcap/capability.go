// Package moderationcap signs and verifies approval-capable moderation
// decisions passed over Kafka. Kafka envelopes identify an actor but do not
// authenticate it; this capability is the application-level proof that a
// current decision came from the configured trust-safety authority.
package moderationcap

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const MinimumKeyBytes = 32

var ErrInvalidCapability = errors.New("invalid moderation capability")

// Claims is the complete immutable decision bound by the signature.
type Claims struct {
	Issuer          string `json:"issuer"`
	Purpose         string `json:"purpose"`
	SubjectID       string `json:"subject_id"`
	ContentRevision int64  `json:"content_revision"`
	Decision        string `json:"decision"`
	Reason          string `json:"reason,omitempty"`
	DecisionID      string `json:"decision_id"`
	PolicyVersion   string `json:"policy_version"`
	// ActorID is optional for legacy story decisions and required by the post
	// moderation protocol. When present it is covered by the same HMAC.
	ActorID       string `json:"actor_id,omitempty"`
	IssuedAtUnix  int64  `json:"issued_at_unix"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
}

type Signer struct {
	key     []byte
	issuer  string
	purpose string
	ttl     time.Duration
	now     func() time.Time
}

func NewSigner(key []byte, issuer, purpose string, ttl time.Duration) (*Signer, error) {
	if len(key) < MinimumKeyBytes {
		return nil, fmt.Errorf("moderation capability key must be at least %d bytes", MinimumKeyBytes)
	}
	if issuer == "" || purpose == "" {
		return nil, errors.New("moderation capability issuer and purpose are required")
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		return nil, errors.New("moderation capability TTL must be in (0,24h]")
	}
	return &Signer{key: append([]byte(nil), key...), issuer: issuer, purpose: purpose, ttl: ttl, now: time.Now}, nil
}

// Sign fills the authority/time claims and returns a detached signature.
func (s *Signer) Sign(c Claims) (Claims, string, error) {
	if s == nil {
		return Claims{}, "", errors.New("moderation capability signer is nil")
	}
	now := s.now().UTC()
	c.Issuer = s.issuer
	c.Purpose = s.purpose
	c.IssuedAtUnix = now.Unix()
	c.ExpiresAtUnix = now.Add(s.ttl).Unix()
	raw, err := json.Marshal(c)
	if err != nil {
		return Claims{}, "", err
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(raw)
	return c, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

type Verifier struct {
	keys      [][]byte
	issuer    string
	purpose   string
	maxTTL    time.Duration
	clockSkew time.Duration
	now       func() time.Time
}

func NewVerifier(activeKey, previousKey []byte, issuer, purpose string, maxTTL time.Duration) (*Verifier, error) {
	if len(activeKey) < MinimumKeyBytes {
		return nil, fmt.Errorf("moderation capability active key must be at least %d bytes", MinimumKeyBytes)
	}
	if len(previousKey) > 0 && len(previousKey) < MinimumKeyBytes {
		return nil, fmt.Errorf("moderation capability previous key must be at least %d bytes", MinimumKeyBytes)
	}
	if issuer == "" || purpose == "" || maxTTL <= 0 || maxTTL > 24*time.Hour {
		return nil, errors.New("invalid moderation capability verifier policy")
	}
	keys := [][]byte{append([]byte(nil), activeKey...)}
	if len(previousKey) > 0 {
		keys = append(keys, append([]byte(nil), previousKey...))
	}
	return &Verifier{keys: keys, issuer: issuer, purpose: purpose, maxTTL: maxTTL, clockSkew: 30 * time.Second, now: time.Now}, nil
}

func (v *Verifier) Verify(c Claims, signature string) error {
	if v == nil || signature == "" {
		return fmt.Errorf("%w: verifier or signature missing", ErrInvalidCapability)
	}
	if c.Issuer != v.issuer || c.Purpose != v.purpose {
		return fmt.Errorf("%w: authority mismatch", ErrInvalidCapability)
	}
	if c.SubjectID == "" || c.ContentRevision <= 0 || c.Decision == "" || c.DecisionID == "" || c.PolicyVersion == "" {
		return fmt.Errorf("%w: required decision claim missing", ErrInvalidCapability)
	}
	issued := time.Unix(c.IssuedAtUnix, 0)
	expires := time.Unix(c.ExpiresAtUnix, 0)
	now := v.now().UTC()
	if expires.Before(issued) || expires.Sub(issued) > v.maxTTL || issued.After(now.Add(v.clockSkew)) || !expires.After(now.Add(-v.clockSkew)) {
		return fmt.Errorf("%w: capability time bounds rejected", ErrInvalidCapability)
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%w: malformed signature", ErrInvalidCapability)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("%w: encode claims", ErrInvalidCapability)
	}
	for _, key := range v.keys {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(raw)
		if hmac.Equal(provided, mac.Sum(nil)) {
			return nil
		}
	}
	return fmt.Errorf("%w: signature mismatch", ErrInvalidCapability)
}
