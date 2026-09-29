package moderationcap

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Post restrictions (Copyright Match plan, sections 3 and 6.2).
//
// A restriction command is a case-aware, signed instruction from
// trust-safety-service to post-service: "place (or release) the hold that
// case C imposes on post P". It is a SEPARATE protocol from Claims above:
// Claims' HMAC is computed over the marshalled struct and that struct is
// shared with the story protocol and the appeal-overturn caller, so it is
// not extended. RestrictionClaims has its own key (POST_RESTRICTION_HMAC_KEY),
// its own purpose and its own audience.
//
// Design rules carried by the claims:
//   - decision_id is minted once per case transition and never changes on
//     a retry; post-service replays the stored outcome for the same digest.
//   - case_revision must increase per case, so a delayed older command can
//     never undo a newer one.
//   - expected_state pins the restriction row's state the issuer believes
//     it is acting on (compare-and-set).
//   - the action / reason_code pairs are a closed table; anything else is
//     refused before it reaches the store.

const (
	// PurposePostRestriction is the purpose every restriction capability carries.
	PurposePostRestriction = "post_restriction"
	// IssuerTrustSafety is the only issuer of restriction commands.
	IssuerTrustSafety = "trust-safety-service"
	// AudiencePostService is the only audience of restriction commands.
	AudiencePostService = "post-service"
	// RestrictionClaimsVersion is the wire version of RestrictionClaims.
	RestrictionClaimsVersion = 1
	// MaxRestrictionTTL bounds a restriction capability's lifetime (section 3).
	MaxRestrictionTTL = 15 * time.Minute

	RestrictionActionPlaceHold   = "place_hold"
	RestrictionActionReleaseHold = "release_hold"

	RestrictionSourceCopyright = "copyright"
	// RestrictionSourceSafety is reserved: structurally valid, refused by
	// post-service with SOURCE_NOT_ENABLED in v1.
	RestrictionSourceSafety = "safety"

	RestrictionStateAbsent   = "absent"
	RestrictionStateActive   = "active"
	RestrictionStateReleased = "released"
)

// ErrInvalidRestrictionClaims means the claims are well signed but name a
// combination the protocol does not allow (post-service answers 422).
var ErrInvalidRestrictionClaims = errors.New("invalid post restriction claims")

// RestrictionClaims is the complete immutable command bound by the signature.
// Field order is the canonical JSON order the HMAC and the digest cover.
type RestrictionClaims struct {
	Version  int    `json:"version"`
	Issuer   string `json:"issuer"`
	Purpose  string `json:"purpose"`
	Audience string `json:"audience"`

	Action       string `json:"action"`
	Source       string `json:"source"`
	CaseID       string `json:"case_id"`
	CaseRevision int64  `json:"case_revision"`

	SubjectID       string `json:"subject_id"`
	SubjectAuthorID string `json:"subject_author_id"`
	ExpectedState   string `json:"expected_state"`

	DecisionID    string `json:"decision_id"`
	PolicyVersion string `json:"policy_version"`
	ReasonCode    string `json:"reason_code"`
	ActorID       string `json:"actor_id"`

	IssuedAtUnix  int64 `json:"issued_at_unix"`
	ExpiresAtUnix int64 `json:"expires_at_unix"`
}

// restrictionReasonTable is the closed action / reason_code table
// (section 6.2). Any other combination is ErrInvalidRestrictionClaims.
var restrictionReasonTable = map[string]map[string]struct{}{
	RestrictionActionPlaceHold: {
		"removal_upheld":       {},
		"reinstated_on_review": {},
	},
	RestrictionActionReleaseHold: {
		"counter_notice_restore": {},
		"rule75_restore":         {},
		"claim_withdrawn":        {},
		"reversed_on_review":     {},
	},
}

// RestrictionReasonAllowed reports whether reason_code is allowed with action.
func RestrictionReasonAllowed(action, reason string) bool {
	reasons, ok := restrictionReasonTable[action]
	if !ok {
		return false
	}
	_, ok = reasons[reason]
	return ok
}

// Validate checks the semantic shape of the claims: every id a UUID, the
// action / reason pair allowed, the state and source in their closed sets,
// the revision positive. It does not check authority, time or signature;
// that is Verify's job, and Verify calls this too.
func (c RestrictionClaims) Validate() error {
	if c.Version != RestrictionClaimsVersion {
		return fmt.Errorf("%w: version %d", ErrInvalidRestrictionClaims, c.Version)
	}
	if !RestrictionReasonAllowed(c.Action, c.ReasonCode) {
		return fmt.Errorf("%w: action %q with reason_code %q", ErrInvalidRestrictionClaims, c.Action, c.ReasonCode)
	}
	if c.Source != RestrictionSourceCopyright && c.Source != RestrictionSourceSafety {
		return fmt.Errorf("%w: source %q", ErrInvalidRestrictionClaims, c.Source)
	}
	switch c.ExpectedState {
	case RestrictionStateAbsent, RestrictionStateActive, RestrictionStateReleased:
	default:
		return fmt.Errorf("%w: expected_state %q", ErrInvalidRestrictionClaims, c.ExpectedState)
	}
	if c.CaseRevision <= 0 {
		return fmt.Errorf("%w: case_revision must be positive", ErrInvalidRestrictionClaims)
	}
	if c.PolicyVersion == "" {
		return fmt.Errorf("%w: policy_version required", ErrInvalidRestrictionClaims)
	}
	for name, raw := range map[string]string{
		"case_id": c.CaseID, "subject_id": c.SubjectID, "subject_author_id": c.SubjectAuthorID,
		"decision_id": c.DecisionID, "actor_id": c.ActorID,
	} {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return fmt.Errorf("%w: %s must be a UUID", ErrInvalidRestrictionClaims, name)
		}
	}
	return nil
}

// Digest is sha256 over the canonical claims with the issued / expires
// times zeroed: two retries of the same decision re-sign with fresh times
// but carry the same digest, while a changed reason, revision or state
// does not. post-service stores it on the event row and compares on replay.
func (c RestrictionClaims) Digest() []byte {
	c.IssuedAtUnix = 0
	c.ExpiresAtUnix = 0
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return sum[:]
}

// RestrictionSigner signs restriction commands. Only trust-safety holds one.
type RestrictionSigner struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewRestrictionSigner builds a signer for issuer trust-safety-service,
// purpose post_restriction, audience post-service. ttl is capped at
// MaxRestrictionTTL.
func NewRestrictionSigner(key []byte, ttl time.Duration) (*RestrictionSigner, error) {
	if len(key) < MinimumKeyBytes {
		return nil, fmt.Errorf("post restriction capability key must be at least %d bytes", MinimumKeyBytes)
	}
	if ttl <= 0 || ttl > MaxRestrictionTTL {
		return nil, fmt.Errorf("post restriction capability TTL must be in (0,%s]", MaxRestrictionTTL)
	}
	return &RestrictionSigner{key: append([]byte(nil), key...), ttl: ttl, now: time.Now}, nil
}

// Sign fills version, authority and time claims, validates the result and
// returns the claims with a detached signature. A semantically invalid
// command is refused here so it is never sent.
func (s *RestrictionSigner) Sign(c RestrictionClaims) (RestrictionClaims, string, error) {
	if s == nil {
		return RestrictionClaims{}, "", errors.New("post restriction signer is nil")
	}
	c = s.stamp(c)
	if err := c.Validate(); err != nil {
		return RestrictionClaims{}, "", err
	}
	return s.signUnchecked(c)
}

// stamp fills the authority and time claims.
func (s *RestrictionSigner) stamp(c RestrictionClaims) RestrictionClaims {
	now := s.now().UTC()
	c.Version = RestrictionClaimsVersion
	c.Issuer = IssuerTrustSafety
	c.Purpose = PurposePostRestriction
	c.Audience = AudiencePostService
	c.IssuedAtUnix = now.Unix()
	c.ExpiresAtUnix = now.Add(s.ttl).Unix()
	return c
}

// signUnchecked signs the claims exactly as given. Sign is the only
// production caller; the tests use it to build a well-signed command with
// invalid semantics.
func (s *RestrictionSigner) signUnchecked(c RestrictionClaims) (RestrictionClaims, string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return RestrictionClaims{}, "", err
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(raw)
	return c, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// RestrictionVerifier verifies restriction commands. Only post-service holds one.
type RestrictionVerifier struct {
	keys      [][]byte
	maxTTL    time.Duration
	clockSkew time.Duration
	now       func() time.Time
}

// NewRestrictionVerifier accepts the active key and, during rotation, one
// previous key. maxTTL is capped at MaxRestrictionTTL.
func NewRestrictionVerifier(activeKey, previousKey []byte, maxTTL time.Duration) (*RestrictionVerifier, error) {
	if len(activeKey) < MinimumKeyBytes {
		return nil, fmt.Errorf("post restriction capability active key must be at least %d bytes", MinimumKeyBytes)
	}
	if len(previousKey) > 0 && len(previousKey) < MinimumKeyBytes {
		return nil, fmt.Errorf("post restriction capability previous key must be at least %d bytes", MinimumKeyBytes)
	}
	if maxTTL <= 0 || maxTTL > MaxRestrictionTTL {
		return nil, fmt.Errorf("post restriction capability max TTL must be in (0,%s]", MaxRestrictionTTL)
	}
	keys := [][]byte{append([]byte(nil), activeKey...)}
	if len(previousKey) > 0 {
		keys = append(keys, append([]byte(nil), previousKey...))
	}
	return &RestrictionVerifier{keys: keys, maxTTL: maxTTL, clockSkew: 30 * time.Second, now: time.Now}, nil
}

// Verify checks authority (issuer, purpose, audience, version), the time
// bounds and the signature. It returns ErrInvalidCapability for those, and
// ErrInvalidRestrictionClaims (wrapped) when the signature is good but the
// signed claims name a combination the protocol refuses; a caller can tell
// the two apart with errors.Is.
func (v *RestrictionVerifier) Verify(c RestrictionClaims, signature string) error {
	if v == nil || signature == "" {
		return fmt.Errorf("%w: verifier or signature missing", ErrInvalidCapability)
	}
	if c.Version != RestrictionClaimsVersion || c.Issuer != IssuerTrustSafety ||
		c.Purpose != PurposePostRestriction || c.Audience != AudiencePostService {
		return fmt.Errorf("%w: authority mismatch", ErrInvalidCapability)
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
	signed := false
	for _, key := range v.keys {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(raw)
		if hmac.Equal(provided, mac.Sum(nil)) {
			signed = true
			break
		}
	}
	if !signed {
		return fmt.Errorf("%w: signature mismatch", ErrInvalidCapability)
	}
	// Semantics last: only a genuinely signed command gets the more
	// descriptive refusal.
	return c.Validate()
}
