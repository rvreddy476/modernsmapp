package moderationcap

import (
	"testing"
	"time"
)

func TestCapabilityBindsEveryApprovalClaim(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, err := NewSigner(key, "trust-safety-service", "story_moderation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(key, nil, "trust-safety-service", "story_moderation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, sig, err := signer.Sign(Claims{SubjectID: "story", ContentRevision: 1, Decision: "approved", Reason: "safe", DecisionID: "decision", PolicyVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(claims, sig); err != nil {
		t.Fatalf("valid capability: %v", err)
	}

	mutations := []func(*Claims){
		func(c *Claims) { c.Issuer = "owner" },
		func(c *Claims) { c.Purpose = "profile_approval" },
		func(c *Claims) { c.SubjectID = "other" },
		func(c *Claims) { c.ContentRevision++ },
		func(c *Claims) { c.Decision = "rejected" },
		func(c *Claims) { c.Reason = "altered" },
		func(c *Claims) { c.DecisionID = "other" },
		func(c *Claims) { c.PolicyVersion = "other" },
	}
	for i, mutate := range mutations {
		altered := claims
		mutate(&altered)
		if err := verifier.Verify(altered, sig); err == nil {
			t.Fatalf("mutation %d retained approval capability", i)
		}
	}
}

func TestCapabilityExpiresAndSupportsOnePreviousKey(t *testing.T) {
	oldKey := []byte("old-0123456789abcdef0123456789abcd")
	newKey := []byte("new-0123456789abcdef0123456789abcd")
	signer, _ := NewSigner(oldKey, "trust", "story", time.Minute)
	base := time.Unix(2_000_000_000, 0)
	signer.now = func() time.Time { return base }
	claims, sig, _ := signer.Sign(Claims{SubjectID: "s", ContentRevision: 1, Decision: "approved", DecisionID: "d", PolicyVersion: "v"})
	verifier, _ := NewVerifier(newKey, oldKey, "trust", "story", time.Minute)
	verifier.now = func() time.Time { return base.Add(10 * time.Second) }
	if err := verifier.Verify(claims, sig); err != nil {
		t.Fatalf("previous key during rotation: %v", err)
	}
	verifier.now = func() time.Time { return base.Add(2 * time.Minute) }
	if err := verifier.Verify(claims, sig); err == nil {
		t.Fatal("expired capability verified")
	}
}
