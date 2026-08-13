package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Module 3 M3-P0-3 / SR-6 — email delivery.
//
// THE DEFECT
//
// auth-service generated verification and password-reset codes, stored them,
// and SENT NOTHING. There was no delivery mechanism in the service at all —
// no SES client, no SMTP, no call to notification-service. `grep` for a
// sender interface returned nothing.
//
// The user-visible consequence: "Forgot password" returned 200 and no code
// ever arrived. Account recovery did not exist. Neither did email
// verification, which meant an address could be registered without the owner
// ever being contacted — so an account could be created against someone
// else's email and that person would never know.
//
// THE FIX
//
// SES via the AWS SDK, credentials resolved by the default chain so IRSA
// supplies them in-cluster. No static access keys are read, accepted or
// stored anywhere in this package.
//
// FAIL CLOSED IN PRODUCTION
//
// A logging sender exists for local development. In production it is REFUSED
// at startup. The reason is specific: a no-op sender is indistinguishable
// from a working one at the API boundary — every call returns nil — so a
// misconfigured production deployment would report healthy while silently
// dropping every verification and recovery email. That is precisely the
// failure being fixed, reintroduced through configuration.

// Message is one outbound email.
type Message struct {
	To      string
	Subject string
	// TextBody is required. HTML is deliberately not supported here: these are
	// security emails, and a text-only body cannot carry a tracking pixel, a
	// remote image, or a link whose visible text differs from its target.
	TextBody string
}

// Validate rejects a message that could not be delivered or that would be a
// security problem if it were.
func (m Message) Validate() error {
	to := strings.TrimSpace(m.To)
	if to == "" {
		return errors.New("email: no recipient")
	}
	// Header injection: a newline in an address lets a caller append
	// arbitrary headers (Bcc:, Reply-To:) to the outbound message.
	if strings.ContainsAny(to, "\r\n") {
		return errors.New("email: recipient contains a line break")
	}
	if !strings.Contains(to, "@") {
		return fmt.Errorf("email: %q is not an address", to)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("email: subject contains a line break")
	}
	if strings.TrimSpace(m.TextBody) == "" {
		return errors.New("email: empty body")
	}
	return nil
}

// Sender delivers one message. An error means NOT DELIVERED — callers must
// surface it rather than assume success.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// LogSender writes the message to the log instead of sending it.
//
// Development only. It logs the FULL body including the code, which is what
// makes local work possible and exactly why it must never run in production.
type LogSender struct {
	Log *slog.Logger
}

func (l LogSender) Send(_ context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	log.Warn("EMAIL NOT SENT — development log sender is active",
		"to", msg.To, "subject", msg.Subject, "body", msg.TextBody)
	return nil
}

// ErrNoSenderConfigured is returned by RefusingSender.
var ErrNoSenderConfigured = errors.New(
	"email delivery is not configured: verification and password-reset messages " +
		"cannot be sent, so account recovery does not work")

// RefusingSender fails every send.
//
// It is what a production process gets when SES is unconfigured, and it is
// deliberately noisy rather than silent: the caller sees an error, the user
// sees a failure, and somebody fixes the configuration. A no-op sender in the
// same position would return nil and drop the mail.
type RefusingSender struct{}

func (RefusingSender) Send(context.Context, Message) error { return ErrNoSenderConfigured }

var (
	_ Sender = LogSender{}
	_ Sender = RefusingSender{}
)
