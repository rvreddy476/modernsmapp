package store

import (
	"testing"

	"github.com/atpost/identity-shared/verification"
)

// The console's "person-verified" is the person bit, whatever the level.
func TestMarksApplicant_IsPersonVerifiedReadsTheBit(t *testing.T) {
	cases := []struct {
		name string
		a    MarksApplicant
		want bool
	}{
		{"person", MarksApplicant{IsVerified: true, VerificationLevel: verification.LevelPerson, BadgeFlags: verification.BadgePersonVerified}, true},
		{"official keeps the bit", MarksApplicant{IsVerified: true, VerificationLevel: verification.LevelOrganisation, BadgeFlags: verification.BadgePersonVerified | verification.BadgeOrganisation}, true},
		{"government keeps the bit", MarksApplicant{IsVerified: true, VerificationLevel: verification.LevelGovernment, BadgeFlags: verification.BadgePersonVerified | verification.BadgeGovernment}, true},
		{"official without the bit", MarksApplicant{IsVerified: true, VerificationLevel: verification.LevelOrganisation, BadgeFlags: verification.BadgeOrganisation}, false},
		{"bit but not verified", MarksApplicant{IsVerified: false, BadgeFlags: verification.BadgePersonVerified}, false},
		{"nothing", MarksApplicant{}, false},
	}
	for _, c := range cases {
		if got := c.a.IsPersonVerified(); got != c.want {
			t.Errorf("%s: IsPersonVerified() = %v, want %v", c.name, got, c.want)
		}
	}
}
