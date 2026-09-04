package config

import (
	"errors"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestResolve pins the boot rules. The important row is "razorpay creds +
// stub flag + no webhook secret": before the extraction that combination
// booted with signature verification off.
func TestResolve(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantMode GatewayMode
		wantErr  error
	}{
		{
			name:     "stub explicitly allowed, no creds",
			env:      map[string]string{"PAYMENTS_ALLOW_STUB": "true"},
			wantMode: ModeStub,
		},
		{
			name:     "stub allowed, webhook secret optional but kept",
			env:      map[string]string{"PAYMENTS_ALLOW_STUB": "true", "RAZORPAY_WEBHOOK_SECRET": "whsec"},
			wantMode: ModeStub,
		},
		{
			name:    "nothing configured refuses to boot",
			env:     map[string]string{},
			wantErr: ErrNoGateway,
		},
		{
			name:    "stub flag must be exactly true",
			env:     map[string]string{"PAYMENTS_ALLOW_STUB": "1"},
			wantErr: ErrNoGateway,
		},
		{
			name: "razorpay creds with webhook secret",
			env: map[string]string{
				"RAZORPAY_KEY_ID": "rzp_test_x", "RAZORPAY_KEY_SECRET": "s", "RAZORPAY_WEBHOOK_SECRET": "whsec",
			},
			wantMode: ModeRazorpay,
		},
		{
			name: "razorpay creds without webhook secret refuses to boot",
			env: map[string]string{
				"RAZORPAY_KEY_ID": "rzp_test_x", "RAZORPAY_KEY_SECRET": "s",
			},
			wantErr: ErrWebhookSecretRequired,
		},
		{
			name: "razorpay creds + stub flag still requires the webhook secret",
			env: map[string]string{
				"RAZORPAY_KEY_ID": "rzp_test_x", "RAZORPAY_KEY_SECRET": "s", "PAYMENTS_ALLOW_STUB": "true",
			},
			wantErr: ErrWebhookSecretRequired,
		},
		{
			name: "razorpay key id without key secret refuses to boot",
			env: map[string]string{
				"RAZORPAY_KEY_ID": "rzp_test_x", "RAZORPAY_WEBHOOK_SECRET": "whsec",
			},
			wantErr: errors.New("any"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Resolve(envMap(tc.env))
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got config %+v", tc.wantErr, cfg)
				}
				if tc.wantErr.Error() != "any" && !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", cfg.Mode, tc.wantMode)
			}
		})
	}
}
