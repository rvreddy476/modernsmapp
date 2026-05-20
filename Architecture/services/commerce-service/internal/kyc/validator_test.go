package kyc

import (
	"context"
	"testing"
)

func TestStubValidator(t *testing.T) {
	v := StubValidator{}
	ctx := context.Background()

	tests := []struct {
		name      string
		in        SellerSnapshot
		wantValid bool
		wantField string // field whose status we want to assert
		wantStat  string // expected status for that field
	}{
		{
			name:      "all empty -> not valid, nothing checked",
			in:        SellerSnapshot{},
			wantValid: false,
		},
		{
			name:      "valid GSTIN only",
			in:        SellerSnapshot{GSTIN: "27ABCDE1234F1Z5"},
			wantValid: true,
			wantField: "gstin",
			wantStat:  "valid",
		},
		{
			name:      "malformed GSTIN",
			in:        SellerSnapshot{GSTIN: "BADGSTIN"},
			wantValid: false,
			wantField: "gstin",
			wantStat:  "invalid",
		},
		{
			name:      "valid PAN + IFSC + bank + UPI",
			in:        SellerSnapshot{PAN: "ABCDE1234F", IFSC: "HDFC0001234", BankAccountNo: "123456789012", UPI: "seller@upi"},
			wantValid: true,
			wantField: "bank_account",
			wantStat:  "valid",
		},
		{
			name:      "non-numeric bank account",
			in:        SellerSnapshot{BankAccountNo: "1234abcd5678"},
			wantValid: false,
			wantField: "bank_account",
			wantStat:  "invalid",
		},
		{
			name:      "too-short bank account",
			in:        SellerSnapshot{BankAccountNo: "12345"},
			wantValid: false,
			wantField: "bank_account",
			wantStat:  "invalid",
		},
		{
			name:      "lowercase PAN normalises",
			in:        SellerSnapshot{PAN: "abcde1234f"},
			wantValid: true,
			wantField: "pan",
			wantStat:  "valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, err := v.Verify(ctx, tt.in)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if rep.AllValid != tt.wantValid {
				t.Errorf("AllValid = %v, want %v (checks=%+v)", rep.AllValid, tt.wantValid, rep.Checks)
			}
			if tt.wantField != "" {
				found := false
				for _, c := range rep.Checks {
					if c.Field == tt.wantField {
						found = true
						if c.Status != tt.wantStat {
							t.Errorf("field %s status = %s, want %s", tt.wantField, c.Status, tt.wantStat)
						}
						if c.Source != "stub" {
							t.Errorf("field %s source = %s, want stub", tt.wantField, c.Source)
						}
					}
				}
				if !found {
					t.Errorf("field %s not in report: %+v", tt.wantField, rep.Checks)
				}
			}
		})
	}
}
