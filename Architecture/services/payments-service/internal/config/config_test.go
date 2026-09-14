package config

import (
	"errors"
	"testing"
	"time"
)

// TestResolveFailedAttemptWindow pins PAYMENTS_FAILED_ATTEMPT_WINDOW: unset
// means 15m, and anything that is not a positive duration refuses to boot in
// either gateway mode.
func TestResolveFailedAttemptWindow(t *testing.T) {
	stub := func(window string) map[string]string {
		return map[string]string{"PAYMENTS_ALLOW_STUB": "true", "PAYMENTS_FAILED_ATTEMPT_WINDOW": window}
	}

	t.Run("unset defaults to 15m", func(t *testing.T) {
		cfg, err := Resolve(envMap(map[string]string{"PAYMENTS_ALLOW_STUB": "true"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.FailedAttemptWindow != 15*time.Minute || DefaultFailedAttemptWindow != 15*time.Minute {
			t.Fatalf("window = %v (default %v), want 15m", cfg.FailedAttemptWindow, DefaultFailedAttemptWindow)
		}
	})

	for raw, want := range map[string]time.Duration{"30m": 30 * time.Minute, " 90s ": 90 * time.Second, "1h": time.Hour} {
		t.Run("valid "+raw, func(t *testing.T) {
			cfg, err := Resolve(envMap(stub(raw)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.FailedAttemptWindow != want {
				t.Fatalf("window = %v, want %v", cfg.FailedAttemptWindow, want)
			}
		})
	}

	for _, raw := range []string{"abc", "15", "0", "0s", "-5m"} {
		t.Run("invalid "+raw, func(t *testing.T) {
			cfg, err := Resolve(envMap(stub(raw)))
			if !errors.Is(err, ErrInvalidFailedAttemptWindow) {
				t.Fatalf("err = %v (config %+v), want ErrInvalidFailedAttemptWindow", err, cfg)
			}
		})
	}

	t.Run("invalid refuses to boot with razorpay credentials too", func(t *testing.T) {
		_, err := Resolve(envMap(map[string]string{
			"RAZORPAY_KEY_ID": "rzp_test_x", "RAZORPAY_KEY_SECRET": "s", "RAZORPAY_WEBHOOK_SECRET": "whsec",
			"PAYMENTS_FAILED_ATTEMPT_WINDOW": "soon",
		}))
		if !errors.Is(err, ErrInvalidFailedAttemptWindow) {
			t.Fatalf("err = %v, want ErrInvalidFailedAttemptWindow", err)
		}
	})
}

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestResolveProductionRequiresServiceCallers pins the production rule (ENV=prod
// exactly, as main.go always read it) and the boot guard: production with no
// caller allowlist refuses to start; every other ENV boots without one.
func TestResolveProductionRequiresServiceCallers(t *testing.T) {
	razorpay := func(extra map[string]string) map[string]string {
		m := map[string]string{
			"RAZORPAY_KEY_ID": "rzp_test_x", "RAZORPAY_KEY_SECRET": "s", "RAZORPAY_WEBHOOK_SECRET": "whsec",
			"INTERNAL_SERVICE_KEY": "k",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	for _, callers := range []string{"", "   "} {
		t.Run("production with SERVICE_CALLERS "+strconvQuote(callers)+" refuses to boot", func(t *testing.T) {
			cfg, err := Resolve(envMap(razorpay(map[string]string{"ENV": "prod", "SERVICE_CALLERS": callers})))
			if !errors.Is(err, ErrServiceCallersRequired) {
				t.Fatalf("err = %v (config %+v), want ErrServiceCallersRequired", err, cfg)
			}
		})
	}

	t.Run("production with callers boots as production", func(t *testing.T) {
		cfg, err := Resolve(envMap(razorpay(map[string]string{"ENV": "prod", "SERVICE_CALLERS": "commerce-service"})))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.Production {
			t.Fatal("ENV=prod resolved as not production")
		}
	})

	for _, env := range []string{"", "dev", "local", "staging", "production", "PROD"} {
		t.Run("ENV "+strconvQuote(env)+" is not production and boots without callers", func(t *testing.T) {
			cfg, err := Resolve(envMap(map[string]string{"ENV": env, "PAYMENTS_ALLOW_STUB": "true"}))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Production {
				t.Fatalf("ENV=%q resolved as production; the rule is ENV=prod exactly", env)
			}
		})
	}
}

func strconvQuote(s string) string { return "\"" + s + "\"" }

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
