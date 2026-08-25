package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Module 3 M3-P0-3 / SR-6 — choosing a sender, and failing closed.
//
// The decision here is which of three senders a process gets, and the
// production branch is the one that matters: an unconfigured production
// deployment must NOT silently fall back to a no-op. A no-op sender is
// indistinguishable from a working one at the call site — every Send returns
// nil — so the service would report healthy while dropping every verification
// and recovery email, which is exactly the failure SR-6 exists to fix.

// Config is the environment-driven sender selection.
type Config struct {
	Production bool
	Region     string
	From       string
	ConfigSet  string
	// AllowLogSender permits the development sender OUTSIDE production. It has
	// no effect in production.
	AllowLogSender bool
}

// LoadConfigFromEnv reads the email configuration.
//
// No AWS credential variables are read. Credentials come from the SDK default
// chain (IRSA in-cluster), so there is nowhere here for a static key to enter.
func LoadConfigFromEnv(production bool) Config {
	return Config{
		Production:     production,
		Region:         strings.TrimSpace(os.Getenv("AWS_REGION")),
		From:           strings.TrimSpace(os.Getenv("SES_FROM_ADDRESS")),
		ConfigSet:      strings.TrimSpace(os.Getenv("SES_CONFIGURATION_SET")),
		AllowLogSender: strings.TrimSpace(os.Getenv("EMAIL_ALLOW_LOG_SENDER")) == "true",
	}
}

// ErrProductionEmailUnconfigured is a startup failure, not a warning.
var ErrProductionEmailUnconfigured = errors.New(
	"SES_FROM_ADDRESS and AWS_REGION must be set in production: without them no " +
		"verification or password-reset email can be delivered, account recovery " +
		"does not work, and an address can be registered without its owner ever " +
		"being contacted")

// NewSender picks the sender for this process.
//
// In production it returns an error rather than a degraded sender, so the
// process refuses to start. A service that boots without email delivery looks
// healthy and locks users out of their own accounts.
func NewSender(ctx context.Context, cfg Config, log *slog.Logger) (Sender, error) {
	if log == nil {
		log = slog.Default()
	}

	configured := cfg.From != "" && cfg.Region != ""

	if cfg.Production {
		if !configured {
			return nil, ErrProductionEmailUnconfigured
		}
		if cfg.AllowLogSender {
			return nil, errors.New(
				"EMAIL_ALLOW_LOG_SENDER=true is not permitted in production: the log " +
					"sender writes verification codes to the log and delivers nothing")
		}
		sender, err := NewSESSender(ctx, SESConfig{
			Region: cfg.Region, From: cfg.From, ConfigurationSet: cfg.ConfigSet,
		})
		if err != nil {
			return nil, fmt.Errorf("production email: %w", err)
		}
		if cfg.ConfigSet == "" {
			// Not fatal, but a real operational hazard: without a configuration
			// set, bounces and complaints are not routed anywhere, sending
			// reputation degrades unseen, and SES eventually throttles the
			// account — at which point nobody receives a recovery email.
			log.Warn("SES_CONFIGURATION_SET is not set — bounce and complaint events " +
				"are not being collected, so deliverability problems will be invisible " +
				"until SES throttles this account")
		}
		log.Info("email delivery: SES", "region", cfg.Region, "from", cfg.From)
		return sender, nil
	}

	// Non-production.
	if configured {
		sender, err := NewSESSender(ctx, SESConfig{
			Region: cfg.Region, From: cfg.From, ConfigurationSet: cfg.ConfigSet,
		})
		if err == nil {
			log.Info("email delivery: SES", "region", cfg.Region, "from", cfg.From)
			return sender, nil
		}
		log.Warn("email delivery: SES configuration present but unusable; "+
			"falling back to the development log sender", "err", err)
	}
	if cfg.AllowLogSender || !configured {
		log.Warn("email delivery: DEVELOPMENT LOG SENDER — no email is actually sent " +
			"and verification codes are written to the log")
		return LogSender{Log: log}, nil
	}
	return RefusingSender{}, nil
}
