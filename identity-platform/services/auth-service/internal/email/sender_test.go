package email

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Module 3 M3-P0-3 / SR-6 — email delivery.
//
// auth-service generated verification and password-reset codes and SENT
// NOTHING: there was no delivery mechanism in the service at all. "Forgot
// password" returned 200 and no code ever arrived, so account recovery did not
// exist.

// The production branch must FAIL rather than degrade. A no-op sender returns
// nil from every Send, so a misconfigured deployment would look healthy while
// dropping every recovery email — the original defect, reintroduced through
// configuration.
func TestProductionRefusesToStartWithoutSESConfiguration(t *testing.T) {
	cases := map[string]Config{
		"nothing configured": {Production: true},
		"region but no from": {Production: true, Region: "ap-south-1"},
		"from but no region": {Production: true, From: "no-reply@example.com"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			sender, err := NewSender(context.Background(), cfg, nil)
			if err == nil {
				t.Fatalf("production started with no email delivery (sender=%T). "+
					"Account recovery would silently not work.", sender)
			}
			if !errors.Is(err, ErrProductionEmailUnconfigured) {
				t.Errorf("error should be the unconfigured sentinel, got %v", err)
			}
		})
	}
}

// The development sender writes verification codes to the log. In production
// that is both a delivery failure and a credential in the logs.
func TestProductionRefusesTheLogSenderEvenWhenExplicitlyEnabled(t *testing.T) {
	cfg := Config{
		Production: true, Region: "ap-south-1", From: "no-reply@example.com",
		AllowLogSender: true,
	}
	sender, err := NewSender(context.Background(), cfg, nil)
	if err == nil {
		t.Fatalf("production accepted the log sender (%T): codes would be written "+
			"to the log and never delivered", sender)
	}
	if !strings.Contains(err.Error(), "EMAIL_ALLOW_LOG_SENDER") {
		t.Errorf("the error should name the offending setting, got %v", err)
	}
}

// Development must stay workable with no AWS account at all.
func TestDevelopmentFallsBackToTheLogSender(t *testing.T) {
	sender, err := NewSender(context.Background(), Config{Production: false}, nil)
	if err != nil {
		t.Fatalf("development requires no configuration: %v", err)
	}
	if _, ok := sender.(LogSender); !ok {
		t.Fatalf("development got %T, want LogSender", sender)
	}
}

// A sender that cannot deliver must return an error, never nil. Returning nil
// is what makes a dropped recovery email invisible.
func TestRefusingSenderReportsFailure(t *testing.T) {
	err := RefusingSender{}.Send(context.Background(), Message{
		To: "someone@example.com", Subject: "s", TextBody: "b",
	})
	if err == nil {
		t.Fatal("RefusingSender reported success; a caller would tell the user their " +
			"code is on its way")
	}
	if !errors.Is(err, ErrNoSenderConfigured) {
		t.Errorf("got %v, want ErrNoSenderConfigured", err)
	}
}

// Header injection: a newline in a recipient or subject lets a caller append
// arbitrary headers (Bcc:, Reply-To:) to a message that carries a login code.
func TestMessageValidationRejectsHeaderInjectionAndEmptyFields(t *testing.T) {
	cases := map[string]Message{
		"no recipient":             {Subject: "s", TextBody: "b"},
		"recipient not an address": {To: "nobody", Subject: "s", TextBody: "b"},
		"newline in recipient":     {To: "a@b.com\nBcc: attacker@evil.com", Subject: "s", TextBody: "b"},
		"CR in recipient":          {To: "a@b.com\rBcc: attacker@evil.com", Subject: "s", TextBody: "b"},
		"newline in subject":       {To: "a@b.com", Subject: "s\nBcc: attacker@evil.com", TextBody: "b"},
		"empty body":               {To: "a@b.com", Subject: "s"},
		"whitespace body":          {To: "a@b.com", Subject: "s", TextBody: "   "},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := msg.Validate(); err == nil {
				t.Fatalf("%s was accepted", name)
			}
			// The senders must apply the same validation, not just the type.
			if err := (LogSender{}).Send(context.Background(), msg); err == nil {
				t.Errorf("LogSender accepted %s", name)
			}
		})
	}

	ok := Message{To: "someone@example.com", Subject: "Your code", TextBody: "123456"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a well-formed message was rejected: %v", err)
	}
}

// A log line that contains a full address is a small privacy leak in its own
// right; masking keeps the domain, which is what a deliverability problem
// actually needs.
func TestMaskAddressHidesTheLocalPart(t *testing.T) {
	cases := map[string]string{
		"someone@example.com": "s******@example.com",
		"a@example.com":       "*@example.com",
		"not-an-address":      "***",
	}
	for in, want := range cases {
		if got := maskAddress(in); got != want {
			t.Errorf("maskAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

// No AWS credential may be read from the environment by this package: a static
// key that cannot be supplied cannot leak through a manifest or an image.
func TestNoStaticAWSCredentialIsReadFromEnv(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLEKEY")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "supersecret")
	t.Setenv("AWS_REGION", "ap-south-1")
	t.Setenv("SES_FROM_ADDRESS", "no-reply@example.com")

	cfg := LoadConfigFromEnv(false)
	if cfg.Region != "ap-south-1" || cfg.From != "no-reply@example.com" {
		t.Fatalf("expected config not loaded: %+v", cfg)
	}
	// The Config type has no field that could hold a credential. If one is
	// ever added, this fails to compile — which is the intent.
	_ = struct {
		Production     bool
		Region         string
		From           string
		ConfigSet      string
		AllowLogSender bool
	}(cfg)
}
