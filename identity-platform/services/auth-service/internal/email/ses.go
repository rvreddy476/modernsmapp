package email

import (
	"context"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// Module 3 M3-P0-3 / SR-6 — SES delivery.
//
// Credentials come from the SDK's default chain, which resolves the IRSA web
// identity token in-cluster. This package deliberately exposes NO way to pass
// an access key or secret: there is no field for one, no environment variable
// read here, and no constructor parameter. A static credential that cannot be
// supplied cannot be leaked in a manifest, a log line or a container image.

// SESSender delivers through Amazon SES v2.
type SESSender struct {
	client *sesv2.Client
	// from must be an address on a verified SES identity.
	from string
	// configurationSet routes bounce and complaint events. Without it, SES
	// accepts a message to a bad address, the bounce goes nowhere, and the
	// sending reputation degrades invisibly until SES throttles the account —
	// at which point NO recovery email is delivered to anyone.
	configurationSet string
}

// SESConfig is the SES-specific configuration.
type SESConfig struct {
	Region string
	// From is the envelope sender, e.g. "no-reply@example.com". Must belong to
	// a verified SES identity or every send fails.
	From string
	// ConfigurationSet is optional but strongly recommended in production.
	ConfigurationSet string
}

// NewSESSender builds the client. It does NOT verify that From is a verified
// identity — that check requires a live API call and would make startup depend
// on SES availability. A misconfigured From surfaces on the first send.
func NewSESSender(ctx context.Context, cfg SESConfig) (*SESSender, error) {
	if strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("ses: From address is required")
	}
	if strings.TrimSpace(cfg.Region) == "" {
		return nil, fmt.Errorf("ses: Region is required")
	}

	// LoadDefaultConfig resolves credentials from the ambient environment. In
	// EKS that is the IRSA web identity token; locally it is whatever the
	// developer's own profile provides. No static key is accepted here.
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("ses: load aws config: %w", err)
	}

	return &SESSender{
		client:           sesv2.NewFromConfig(awsCfg),
		from:             cfg.From,
		configurationSet: cfg.ConfigurationSet,
	}, nil
}

func (s *SESSender) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}

	in := &sesv2.SendEmailInput{
		FromEmailAddress: &s.from,
		Destination:      &types.Destination{ToAddresses: []string{msg.To}},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: &msg.Subject},
				Body:    &types.Body{Text: &types.Content{Data: &msg.TextBody}},
			},
		},
	}
	if s.configurationSet != "" {
		in.ConfigurationSetName = &s.configurationSet
	}

	if _, err := s.client.SendEmail(ctx, in); err != nil {
		// The error is returned, never swallowed. A caller that treats a send
		// failure as success recreates the original defect: the user is told
		// their reset email is on the way and it never arrives.
		return fmt.Errorf("ses: send to %s: %w", maskAddress(msg.To), err)
	}
	return nil
}

// maskAddress keeps the domain (useful for diagnosing a whole-domain
// deliverability problem) and hides the local part.
func maskAddress(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		return "***"
	}
	local := addr[:at]
	if len(local) <= 1 {
		return "*" + addr[at:]
	}
	return local[:1] + strings.Repeat("*", len(local)-1) + addr[at:]
}

var _ Sender = (*SESSender)(nil)
