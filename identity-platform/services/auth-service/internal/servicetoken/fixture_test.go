package servicetoken

import (
	"errors"
	"testing"
	"time"
)

// TestSharedPackageFixtureVerifies: a token minted by the SHARED package
// (Architecture/shared/servicetoken) verifies here unchanged. This is the
// byte-compatibility contract: if the shared wire format moves, the fixture
// is regenerated and this test breaks until the local verifier follows.
func TestSharedPackageFixtureVerifies(t *testing.T) {
	v := NewVerifier(sharedFixtureAud)
	if err := v.RegisterBase64(sharedFixtureIssuer, sharedFixtureKID, sharedFixturePubB64, []string{sharedFixtureScope}); err != nil {
		t.Fatal(err)
	}
	v.SetClock(func() time.Time { return time.Unix(sharedFixtureIAT+1, 0) })
	got, err := v.Verify(sharedFixtureToken, sharedFixtureScope)
	if err != nil {
		t.Fatalf("shared-package token refused: %v", err)
	}
	if got.Issuer != sharedFixtureIssuer || got.Actor != sharedFixtureActor || got.JTI != sharedFixtureJTI {
		t.Fatalf("verified = %+v", got)
	}
	if got.ExpiresAt.Unix() != sharedFixtureIAT+60 {
		t.Fatalf("expires_at = %d want iat+60", got.ExpiresAt.Unix())
	}
	// The same token, once its minute has passed, is expired here as there.
	v.SetClock(func() time.Time { return time.Unix(sharedFixtureIAT+61, 0) })
	if _, err := v.Verify(sharedFixtureToken, sharedFixtureScope); !errors.Is(err, ErrExpired) {
		t.Fatalf("after expiry: got %v want ErrExpired", err)
	}
	// And a scope the shared package did not sign is not in it.
	v.SetClock(func() time.Time { return time.Unix(sharedFixtureIAT+1, 0) })
	if _, err := v.Verify(sharedFixtureToken, "platform:users.suspend"); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("foreign scope: got %v want ErrScopeDenied", err)
	}
}
