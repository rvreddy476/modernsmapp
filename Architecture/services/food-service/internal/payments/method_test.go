package payments

import (
	"errors"
	"testing"
)

func TestResolveMethod(t *testing.T) {
	off := Flags{}
	on := Flags{CODEnabled: true, WalletEnabled: true}
	cases := []struct {
		name       string
		raw        string
		flags      Flags
		store      string
		instrument string
		err        error
	}{
		{"upi is online", "upi", off, StoreOnline, "upi", nil},
		{"card is online", "card", off, StoreOnline, "card", nil},
		{"cod refused when the flag is off", "COD", off, "", "", ErrPaymentMethodUnavailable},
		{"lower-case cod refused when the flag is off", "cod", off, "", "", ErrPaymentMethodUnavailable},
		{"wallet refused when the flag is off", "WALLET", off, "", "", ErrPaymentMethodUnavailable},
		{"cod allowed with the flag on", "cod", on, StoreCOD, "", nil},
		{"wallet allowed with the flag on", "wallet", on, StoreWallet, "", nil},
		{"no method never defaults to cod", "", off, "", "", ErrPaymentMethodInvalid},
		{"no method never defaults to cod even with cod on", "", on, "", "", ErrPaymentMethodInvalid},
		{"legacy ONLINE is not an instrument", "ONLINE", off, "", "", ErrPaymentMethodInvalid},
		{"net_banking is not a launch method", "net_banking", off, "", "", ErrPaymentMethodInvalid},
		{"UPI is not canonical", "UPI", off, "", "", ErrPaymentMethodInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveMethod(tc.raw, tc.flags)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("ResolveMethod(%q) error = %v, want %v", tc.raw, err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveMethod(%q) unexpected error %v", tc.raw, err)
			}
			if got.Store != tc.store || got.Instrument != tc.instrument {
				t.Fatalf("ResolveMethod(%q) = %+v, want store=%s instrument=%s", tc.raw, got, tc.store, tc.instrument)
			}
		})
	}
}

func TestFlagsFromEnv(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if f := FlagsFromEnv(get(nil)); f.CODEnabled || f.WalletEnabled {
		t.Fatalf("defaults must be off, got %+v", f)
	}
	if f := FlagsFromEnv(get(map[string]string{"FOOD_COD_ENABLED": "true", "FOOD_WALLET_PAYMENTS_ENABLED": "1"})); !f.CODEnabled || !f.WalletEnabled {
		t.Fatalf("explicit true not honoured, got %+v", f)
	}
	if f := FlagsFromEnv(get(map[string]string{"FOOD_COD_ENABLED": "yes please"})); f.CODEnabled {
		t.Fatalf("an unparseable flag must fail closed, got %+v", f)
	}
}
