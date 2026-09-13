package payout

import (
	"context"
	"testing"
)

func TestDisabledVerifierNeverVerifies(t *testing.T) {
	v, err := DisabledVerifier{}.Verify(context.Background(), BankAccountDetails{
		HolderName: "Test Holder", AccountNumber: "000123456789", IFSC: "HDFC0000053",
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Status != StatusNotVerified {
		t.Fatalf("status = %s, want %s", v.Status, StatusNotVerified)
	}
	if v.Reason != ReasonVerificationPendingOps {
		t.Fatalf("reason = %q, want %q", v.Reason, ReasonVerificationPendingOps)
	}
	if v.VerifiedName != "" {
		t.Fatalf("a disabled verifier must not claim a verified name")
	}
}

func TestVerifierFromEnv(t *testing.T) {
	cases := []struct {
		raw          string
		wantErr      bool
		wantDisabled bool
	}{
		{"", false, true},
		{"false", false, true},
		{"FALSE", false, true},
		{"0", false, true},
		{"true", true, false},
		{"1", true, false},
		{"maybe", true, false},
	}
	for _, tc := range cases {
		v, err := VerifierFromEnv(func(k string) string {
			if k == "FOOD_PENNY_DROP_ENABLED" {
				return tc.raw
			}
			return ""
		})
		if (err != nil) != tc.wantErr {
			t.Fatalf("%q: err = %v", tc.raw, err)
		}
		if _, ok := v.(DisabledVerifier); ok != tc.wantDisabled {
			t.Fatalf("%q: disabled = %v, want %v", tc.raw, ok, tc.wantDisabled)
		}
	}
}
