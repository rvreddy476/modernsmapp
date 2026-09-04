// Package config resolves payments-service's boot configuration from the
// environment and enforces the invariants that used to be scattered
// through cmd/server/main.go. Extracted so the "refuse to boot" rules are
// unit-testable without spawning the binary.
package config

import (
	"errors"
	"fmt"
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
}

var (
	ErrNoGateway = errors.New("RAZORPAY_KEY_ID is required in production; set PAYMENTS_ALLOW_STUB=true for dev/test")
	// ErrWebhookSecretRequired fires whenever the Razorpay gateway is
	// selected without a webhook secret — regardless of PAYMENTS_ALLOW_STUB.
	// Previously the secret check was keyed on the stub flag, so a deploy
	// with real credentials AND PAYMENTS_ALLOW_STUB=true (the docker-compose
	// default) would boot with signature verification switched off and
	// accept forged payment.captured webhooks.
	ErrWebhookSecretRequired = errors.New("RAZORPAY_WEBHOOK_SECRET is required when running with the Razorpay gateway")
)

// Resolve reads the environment through getenv (os.Getenv in production,
// a map lookup in tests) and applies the boot rules:
//
//  1. RAZORPAY_KEY_ID set → ModeRazorpay; RAZORPAY_WEBHOOK_SECRET is
//     mandatory. PAYMENTS_ALLOW_STUB is ignored.
//  2. RAZORPAY_KEY_ID empty and PAYMENTS_ALLOW_STUB=true → ModeStub;
//     the webhook secret is optional.
//  3. Otherwise → ErrNoGateway; the process must not start.
func Resolve(getenv func(string) string) (Config, error) {
	cfg := Config{
		RazorpayKeyID:     getenv("RAZORPAY_KEY_ID"),
		RazorpayKeySecret: getenv("RAZORPAY_KEY_SECRET"),
		WebhookSecret:     getenv("RAZORPAY_WEBHOOK_SECRET"),
		InternalKey:       getenv("INTERNAL_SERVICE_KEY"),
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
