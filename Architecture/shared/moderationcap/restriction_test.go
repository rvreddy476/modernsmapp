package moderationcap

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var restrictionTestKey = []byte("post-restriction-0123456789abcdef0123")

func validRestriction() RestrictionClaims {
	return RestrictionClaims{
		Action: RestrictionActionPlaceHold, Source: RestrictionSourceCopyright,
		CaseID: uuid.NewString(), CaseRevision: 7,
		SubjectID: uuid.NewString(), SubjectAuthorID: uuid.NewString(), ExpectedState: RestrictionStateAbsent,
		DecisionID: uuid.NewString(), PolicyVersion: "copyright-v1", ReasonCode: "removal_upheld",
		ActorID: uuid.NewString(),
	}
}

func TestRestrictionCapabilityBindsEveryClaim(t *testing.T) {
	signer, err := NewRestrictionSigner(restrictionTestKey, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewRestrictionVerifier(restrictionTestKey, nil, MaxRestrictionTTL)
	if err != nil {
		t.Fatal(err)
	}
	claims, sig, err := signer.Sign(validRestriction())
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != IssuerTrustSafety || claims.Purpose != PurposePostRestriction || claims.Audience != AudiencePostService || claims.Version != 1 {
		t.Fatalf("signer did not stamp authority: %+v", claims)
	}
	if err := verifier.Verify(claims, sig); err != nil {
		t.Fatalf("valid capability: %v", err)
	}
	other := uuid.NewString()
	mutations := map[string]func(*RestrictionClaims){
		"issuer":            func(c *RestrictionClaims) { c.Issuer = "admin-service" },
		"purpose":           func(c *RestrictionClaims) { c.Purpose = "post_moderation" },
		"audience":          func(c *RestrictionClaims) { c.Audience = "media-service" },
		"version":           func(c *RestrictionClaims) { c.Version = 2 },
		"action":            func(c *RestrictionClaims) { c.Action = RestrictionActionReleaseHold; c.ReasonCode = "claim_withdrawn" },
		"source":            func(c *RestrictionClaims) { c.Source = RestrictionSourceSafety },
		"case_id":           func(c *RestrictionClaims) { c.CaseID = other },
		"case_revision":     func(c *RestrictionClaims) { c.CaseRevision++ },
		"subject_id":        func(c *RestrictionClaims) { c.SubjectID = other },
		"subject_author_id": func(c *RestrictionClaims) { c.SubjectAuthorID = other },
		"expected_state":    func(c *RestrictionClaims) { c.ExpectedState = RestrictionStateReleased },
		"decision_id":       func(c *RestrictionClaims) { c.DecisionID = other },
		"policy_version":    func(c *RestrictionClaims) { c.PolicyVersion = "copyright-v2" },
		"reason_code":       func(c *RestrictionClaims) { c.ReasonCode = "reinstated_on_review" },
		"actor_id":          func(c *RestrictionClaims) { c.ActorID = other },
		"issued_at":         func(c *RestrictionClaims) { c.IssuedAtUnix-- },
		"expires_at":        func(c *RestrictionClaims) { c.ExpiresAtUnix++ },
	}
	for name, mutate := range mutations {
		altered := claims
		mutate(&altered)
		if err := verifier.Verify(altered, sig); err == nil {
			t.Errorf("mutation %s retained the capability", name)
		}
	}
}

func TestRestrictionCapabilityRefusesTheStoryAndModerationKeys(t *testing.T) {
	// The appeal protocol's key must never sign a restriction: a different
	// key, and a Claims signer cannot produce RestrictionClaims at all.
	postModerationKey := []byte("post-moderation-0123456789abcdef01234")
	signer, _ := NewRestrictionSigner(postModerationKey, time.Minute)
	verifier, _ := NewRestrictionVerifier(restrictionTestKey, nil, MaxRestrictionTTL)
	claims, sig, err := signer.Sign(validRestriction())
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(claims, sig); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("other key verified: %v", err)
	}
}

func TestRestrictionCapabilityTTLAndRotation(t *testing.T) {
	if _, err := NewRestrictionSigner(restrictionTestKey, MaxRestrictionTTL+time.Second); err == nil {
		t.Fatal("signer accepted a TTL over 15 minutes")
	}
	if _, err := NewRestrictionVerifier(restrictionTestKey, nil, time.Hour); err == nil {
		t.Fatal("verifier accepted a max TTL over 15 minutes")
	}
	oldKey := []byte("old-restriction-0123456789abcdef01234")
	signer, _ := NewRestrictionSigner(oldKey, time.Minute)
	base := time.Unix(2_000_000_000, 0)
	signer.now = func() time.Time { return base }
	claims, sig, err := signer.Sign(validRestriction())
	if err != nil {
		t.Fatal(err)
	}
	verifier, _ := NewRestrictionVerifier(restrictionTestKey, oldKey, MaxRestrictionTTL)
	verifier.now = func() time.Time { return base.Add(10 * time.Second) }
	if err := verifier.Verify(claims, sig); err != nil {
		t.Fatalf("previous key during rotation: %v", err)
	}
	verifier.now = func() time.Time { return base.Add(2 * time.Minute) }
	if err := verifier.Verify(claims, sig); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("expired capability: %v", err)
	}
}

func TestRestrictionClaimsSemantics(t *testing.T) {
	signer, _ := NewRestrictionSigner(restrictionTestKey, time.Minute)
	verifier, _ := NewRestrictionVerifier(restrictionTestKey, nil, MaxRestrictionTTL)
	bad := map[string]func(*RestrictionClaims){
		"release with a place reason":  func(c *RestrictionClaims) { c.Action = RestrictionActionReleaseHold },
		"place with a release reason":  func(c *RestrictionClaims) { c.ReasonCode = "claim_withdrawn" },
		"unknown action":               func(c *RestrictionClaims) { c.Action = "suspend" },
		"unknown reason":               func(c *RestrictionClaims) { c.ReasonCode = "because" },
		"unknown source":               func(c *RestrictionClaims) { c.Source = "dmca" },
		"unknown state":                func(c *RestrictionClaims) { c.ExpectedState = "pending" },
		"zero revision":                func(c *RestrictionClaims) { c.CaseRevision = 0 },
		"empty policy":                 func(c *RestrictionClaims) { c.PolicyVersion = "" },
		"case_id not a uuid":           func(c *RestrictionClaims) { c.CaseID = "case-1" },
		"subject_id not a uuid":        func(c *RestrictionClaims) { c.SubjectID = "post" },
		"decision_id nil uuid":         func(c *RestrictionClaims) { c.DecisionID = uuid.Nil.String() },
		"actor_id missing":             func(c *RestrictionClaims) { c.ActorID = "" },
		"subject_author_id not a uuid": func(c *RestrictionClaims) { c.SubjectAuthorID = "x" },
	}
	for name, mutate := range bad {
		c := validRestriction()
		mutate(&c)
		if _, _, err := signer.Sign(c); !errors.Is(err, ErrInvalidRestrictionClaims) {
			t.Errorf("signer accepted %s: %v", name, err)
		}
	}
	// A signed-but-invalid command (a bug on the issuer side, or a hand-built
	// body) is distinguishable from a forgery.
	good := validRestriction()
	claims, _, _ := signer.Sign(good)
	claims.ReasonCode = "claim_withdrawn" // now place_hold + release reason
	forged := RestrictionSigner{key: restrictionTestKey, ttl: time.Minute, now: time.Now}
	raw, sig := signRaw(t, &forged, claims)
	if err := verifier.Verify(raw, sig); !errors.Is(err, ErrInvalidRestrictionClaims) || errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("signed invalid claims: %v, want ErrInvalidRestrictionClaims", err)
	}
	// Safety is structurally valid (reserved); post-service refuses it.
	c := validRestriction()
	c.Source = RestrictionSourceSafety
	if _, _, err := signer.Sign(c); err != nil {
		t.Fatalf("safety source must be structurally valid: %v", err)
	}
}

func TestRestrictionDigestIgnoresTimesOnly(t *testing.T) {
	signer, _ := NewRestrictionSigner(restrictionTestKey, time.Minute)
	c := validRestriction()
	first, _, _ := signer.Sign(c)
	signer.now = func() time.Time { return time.Now().Add(time.Minute) }
	retry, _, _ := signer.Sign(c)
	if string(first.Digest()) != string(retry.Digest()) {
		t.Fatal("a retry with fresh times changed the digest")
	}
	changed := c
	changed.ReasonCode = "reinstated_on_review"
	other, _, _ := signer.Sign(changed)
	if string(first.Digest()) == string(other.Digest()) {
		t.Fatal("a changed reason kept the digest")
	}
	if len(first.Digest()) != 32 {
		t.Fatalf("digest length %d", len(first.Digest()))
	}
}

// signRaw signs claims exactly as given (no stamping, no validation) so a
// test can produce a well-signed but semantically invalid command.
func signRaw(t *testing.T, s *RestrictionSigner, c RestrictionClaims) (RestrictionClaims, string) {
	t.Helper()
	stamped, sig, err := s.signUnchecked(c)
	if err != nil {
		t.Fatal(err)
	}
	return stamped, sig
}

// The key is shared by issuer and verifier, so a command signed under it
// for the wrong audience, purpose, issuer or version must still be
// refused: the authority claims are checked, not merely covered.
func TestRestrictionCapabilityRefusesWrongAuthorityEvenWhenSigned(t *testing.T) {
	signer, _ := NewRestrictionSigner(restrictionTestKey, time.Minute)
	verifier, _ := NewRestrictionVerifier(restrictionTestKey, nil, MaxRestrictionTTL)
	stamped := signer.stamp(validRestriction())
	for name, mutate := range map[string]func(*RestrictionClaims){
		"audience": func(c *RestrictionClaims) { c.Audience = "media-service" },
		"purpose":  func(c *RestrictionClaims) { c.Purpose = "post_moderation" },
		"issuer":   func(c *RestrictionClaims) { c.Issuer = "admin-service" },
		"version":  func(c *RestrictionClaims) { c.Version = 2 },
	} {
		c := stamped
		mutate(&c)
		claims, sig, err := signer.signUnchecked(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifier.Verify(claims, sig); !errors.Is(err, ErrInvalidCapability) {
			t.Errorf("%s: well-signed wrong authority verified: %v", name, err)
		}
	}
}
