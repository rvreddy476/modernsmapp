package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
)

// The PAN item in both modes of MEMBERSHIPS_REQUIRE_PAN_VERIFIED. Strict
// (true, every deploy): verified_at must be set. Lenient (false, dev): a PAN
// on file that passes kyc.ValidatePAN also counts. Every other item is
// satisfied by the rig, so Missing is exactly the PAN item or nothing.
func TestEligibilityPANModes(t *testing.T) {
	ctx := context.Background()
	verified := t0.Add(-time.Hour)
	str := func(s string) *string { return &s }
	cases := []struct {
		name      string
		profile   *postgres.CreatorTaxProfile // nil = no row
		strictOK  bool
		lenientOK bool
	}{
		{"no tax profile", nil, false, false},
		{"profile without a PAN", &postgres.CreatorTaxProfile{}, false, false},
		{"blank PAN", &postgres.CreatorTaxProfile{PANEncrypted: str("   ")}, false, false},
		{"well-formed PAN, not verified", &postgres.CreatorTaxProfile{PANEncrypted: str("ABCPE1234F")}, false, true},
		{"well-formed PAN in lower case with spaces, not verified", &postgres.CreatorTaxProfile{PANEncrypted: str("  abcpe1234f ")}, false, true},
		{"wrong layout, not verified", &postgres.CreatorTaxProfile{PANEncrypted: str("ABCPE12345")}, false, false},
		{"unknown holder type, not verified", &postgres.CreatorTaxProfile{PANEncrypted: str("ABCXE1234F")}, false, false},
		{"ciphertext-looking value, not verified", &postgres.CreatorTaxProfile{PANEncrypted: str("enc:pan")}, false, false},
		{"verified PAN", &postgres.CreatorTaxProfile{PANEncrypted: str("ABCPE1234F"), VerifiedAt: &verified}, true, true},
		{"verified_at without a PAN", &postgres.CreatorTaxProfile{VerifiedAt: &verified}, false, false},
		{"verified_at with a blank PAN", &postgres.CreatorTaxProfile{PANEncrypted: str(" "), VerifiedAt: &verified}, false, false},
	}
	for _, tc := range cases {
		for _, mode := range []struct {
			name           string
			formatSuffices bool
			wantOK         bool
		}{
			{"require verified", false, tc.strictOK},
			{"format suffices", true, tc.lenientOK},
		} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				r := newRig(t0)
				r.fake.AllPANVerified = false
				if tc.profile != nil {
					p := *tc.profile
					p.UserID = creatorUnderTest
					r.fake.Tax[creatorUnderTest] = &p
				}
				r.svc.eligibility.PANFormatSuffices = mode.formatSuffices
				el, err := r.svc.MembershipEligibility(ctx, creatorUnderTest)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"pan"}
				if mode.wantOK {
					want = []string{}
				}
				if !reflect.DeepEqual(el.Missing, want) || el.Eligible != mode.wantOK {
					t.Fatalf("eligibility = %+v, want missing %v", el, want)
				}
			})
		}
	}
}

// The zero value of EligibilitySources is strict: a service wired without
// the flag never accepts an unverified PAN.
func TestEligibilityPANDefaultIsStrict(t *testing.T) {
	if (EligibilitySources{}).PANFormatSuffices {
		t.Fatal("EligibilitySources zero value must require verified_at")
	}
	r := newRig(t0)
	r.fake.AllPANVerified = false
	pan := "ABCPE1234F"
	r.fake.Tax[creatorUnderTest] = &postgres.CreatorTaxProfile{UserID: creatorUnderTest, PANEncrypted: &pan}
	r.svc.WithEligibilitySources(EligibilitySources{BirthDates: r.elig, Followers: r.elig, Standing: r.elig})
	el, err := r.svc.MembershipEligibility(context.Background(), creatorUnderTest)
	if err != nil {
		t.Fatal(err)
	}
	if el.Eligible || !reflect.DeepEqual(el.Missing, []string{"pan"}) {
		t.Fatalf("eligibility = %+v, want the PAN item missing", el)
	}
}
