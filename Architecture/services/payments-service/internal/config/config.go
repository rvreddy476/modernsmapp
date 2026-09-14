// Package config resolves payments-service's boot configuration from the
// environment and enforces the invariants that used to be scattered
// through cmd/server/main.go. Extracted so the "refuse to boot" rules are
// unit-testable without spawning the binary.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// GatewayMode names which PaymentGateway implementation boot selected.
type GatewayMode string

const (
	// ModeRazorpay means real credentials were supplied; real money moves.
	ModeRazorpay GatewayMode = "razorpay"
	// ModeStub means no credentials and PAYMENTS_ALLOW_STUB=true; the
	// StubGateway accepts every signature and never contacts a PSP.
	ModeStub GatewayMode = "stub"
)

// Config is the resolved, validated boot configuration.
type Config struct {
	Mode GatewayMode

	RazorpayKeyID     string
	RazorpayKeySecret string
	// WebhookSecret is the HMAC key for X-Razorpay-Signature. Mandatory in
	// ModeRazorpay; optional (signature checks off) in ModeStub.
	WebhookSecret string

	InternalKey string

	// Production is ENV=prod, the rule cmd/server/main.go has always used
	// (env("ENV", "dev") == "prod"): a blank ENV, dev and staging are not
	// production. In production PAYMENTS_ALLOW_STUB is refused (main.go),
	// SERVICE_CALLERS is mandatory (ErrServiceCallersRequired) and the refund
	// operator routes refuse the legacy internal key.
	Production bool

	// FailedAttemptWindow is how long an intent whose order has only failed
	// payment attempts (or none) stays pending so the customer can retry on
	// the same provider order. Only after the order has been quiet this long
	// does the reconciler finalise the intent FAILED and publish
	// payment.failed. PAYMENTS_FAILED_ATTEMPT_WINDOW, a Go duration.
	FailedAttemptWindow time.Duration
}

// DefaultFailedAttemptWindow applies when PAYMENTS_FAILED_ATTEMPT_WINDOW is
// unset.
const DefaultFailedAttemptWindow = 15 * time.Minute

var (
	// ErrInvalidFailedAttemptWindow refuses a window that does not parse or
	// is not positive. A zero window would finalise FAILED on the first
	// failed attempt, which is exactly the lost-retry defect it exists for.
	ErrInvalidFailedAttemptWindow = errors.New("PAYMENTS_FAILED_ATTEMPT_WINDOW must be a positive Go duration such as 15m")
	ErrNoGateway = errors.New("RAZORPAY_KEY_ID is required in production; set PAYMENTS_ALLOW_STUB=true for dev/test")
	// ErrWebhookSecretRequired fires whenever the Razorpay gateway is
	// selected without a webhook secret — regardless of PAYMENTS_ALLOW_STUB.
	// Previously the secret check was keyed on the stub flag, so a deploy
	// with real credentials AND PAYMENTS_ALLOW_STUB=true (the docker-compose
	// default) would boot with signature verification switched off and
	// accept forged payment.captured webhooks.
	ErrWebhookSecretRequired = errors.New("RAZORPAY_WEBHOOK_SECRET is required when running with the Razorpay gateway")
	// ErrServiceCallersRequired refuses a production boot with no caller
	// allowlist. Without one the shared INTERNAL_SERVICE_KEY would be the only
	// credential on /v1/payments/internal, every sibling service would hold
	// every domain's authority, and the refund operator routes (which refuse
	// that key in production) could not be reached by anyone.
	ErrServiceCallersRequired = errors.New("SERVICE_CALLERS is required when ENV=prod: register each calling service " +
		"(SERVICE_CALLER_<NAME>_KID, _PUBKEY, _OPS, _REFTYPES); the internal service key alone is not a production credential")
)

// Resolve reads the environment through getenv (os.Getenv in production,
// a map lookup in tests) and applies the boot rules:
//
//  1. RAZORPAY_KEY_ID set → ModeRazorpay; RAZORPAY_WEBHOOK_SECRET is
//     mandatory. PAYMENTS_ALLOW_STUB is ignored.
//  2. RAZORPAY_KEY_ID empty and PAYMENTS_ALLOW_STUB=true → ModeStub;
//     the webhook secret is optional.
//  3. Otherwise → ErrNoGateway; the process must not start.
//
// In every mode PAYMENTS_FAILED_ATTEMPT_WINDOW, when set, must parse as a
// positive duration (ErrInvalidFailedAttemptWindow); unset means
// DefaultFailedAttemptWindow.
//
// With ENV=prod, a blank SERVICE_CALLERS is ErrServiceCallersRequired. A
// non-blank but invalid allowlist is refused by main.go's verifier build in
// every environment.
func Resolve(getenv func(string) string) (Config, error) {
	cfg := Config{
		RazorpayKeyID:       getenv("RAZORPAY_KEY_ID"),
		RazorpayKeySecret:   getenv("RAZORPAY_KEY_SECRET"),
		WebhookSecret:       getenv("RAZORPAY_WEBHOOK_SECRET"),
		InternalKey:         getenv("INTERNAL_SERVICE_KEY"),
		Production:          getenv("ENV") == "prod",
		FailedAttemptWindow: DefaultFailedAttemptWindow,
	}
	if cfg.Production && strings.TrimSpace(getenv("SERVICE_CALLERS")) == "" {
		return Config{}, ErrServiceCallersRequired
	}
	if raw := strings.TrimSpace(getenv("PAYMENTS_FAILED_ATTEMPT_WINDOW")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("%w (got %q)", ErrInvalidFailedAttemptWindow, raw)
		}
		cfg.FailedAttemptWindow = d
	}
	allowStub := getenv("PAYMENTS_ALLOW_STUB") == "true"

	switch {
	case cfg.RazorpayKeyID != "":
		cfg.Mode = ModeRazorpay
		if cfg.RazorpayKeySecret == "" {
			return Config{}, fmt.Errorf("RAZORPAY_KEY_SECRET is required when RAZORPAY_KEY_ID is set")
		}
		if cfg.WebhookSecret == "" {
			return Config{}, ErrWebhookSecretRequired
		}
	case allowStub:
		cfg.Mode = ModeStub
	default:
		return Config{}, ErrNoGateway
	}
	return cfg, nil
}
