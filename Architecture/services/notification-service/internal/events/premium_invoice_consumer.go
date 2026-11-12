package events

// Momentum Premium tax documents (13 Nov 2026): premium.invoice.issued and
// premium.credit_note.issued from monetization-service's invoice-row outbox
// on monetization.events. Payload ids only: user_id, invoice_id, pass_id,
// kind. Own consumer group; every other type on the topic is ignored.
//
// Each document event queues the buyer's e-mail (service.
// QueuePremiumInvoiceEmail, premium_invoice_email.go) and is committed only
// once the job row is written (processAndCommit); the queue key is the
// (event type, invoice id) pair, so a replay never sends twice. A message
// that cannot be decoded, or names no buyer or document, is logged and
// committed: it would fail the same way forever.

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// premiumInvoicePayload is monetization's PremiumInvoicePayload.
type premiumInvoicePayload struct {
	UserID    string `json:"user_id"`
	InvoiceID string `json:"invoice_id"`
	PassID    string `json:"pass_id"`
	Kind      string `json:"kind"`
}

// premiumInvoiceMailer is the service seam; *service.Service satisfies it.
type premiumInvoiceMailer interface {
	QueuePremiumInvoiceEmail(ctx context.Context, ev service.PremiumInvoiceEvent) error
}

// PremiumInvoiceConsumer reads monetization.events for document e-mails.
type PremiumInvoiceConsumer struct {
	reader membershipReader
	mail   premiumInvoiceMailer
}

// NewPremiumInvoiceConsumerWithDialer builds the consumer on its own group.
func NewPremiumInvoiceConsumerWithDialer(brokers []string, groupID, topic string, svc *service.Service, dialer *kafka.Dialer) *PremiumInvoiceConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	c := &PremiumInvoiceConsumer{reader: reader}
	if svc != nil { // never store a typed nil in the interface
		c.mail = svc
	}
	return c
}

// Start reads until ctx ends, one message at a time.
func (c *PremiumInvoiceConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("premium invoice consumer shutting down")
			} else {
				slog.Error("premium invoice consumer fetch failed", "error", err)
			}
			return
		}
		if !processAndCommit(ctx, c.reader, m, c.processMessage) {
			return
		}
	}
}

// Close closes the reader.
func (c *PremiumInvoiceConsumer) Close() error { return c.reader.Close() }

// planPremiumInvoiceEmail decodes one envelope. ok is false for another
// event type, or a document event that names no buyer or document, or
// whose kind does not match its type.
func planPremiumInvoiceEmail(env events.EventEnvelope) (service.PremiumInvoiceEvent, bool) {
	kind := service.PremiumKindFor(env.EventType)
	if kind == "" {
		return service.PremiumInvoiceEvent{}, false
	}
	var p premiumInvoicePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return service.PremiumInvoiceEvent{}, false
	}
	user, errU := uuid.Parse(p.UserID)
	inv, errI := uuid.Parse(p.InvoiceID)
	if errU != nil || errI != nil || user == uuid.Nil || inv == uuid.Nil || p.Kind != kind {
		return service.PremiumInvoiceEvent{}, false
	}
	pass, _ := uuid.Parse(p.PassID)
	return service.PremiumInvoiceEvent{EventType: env.EventType, UserID: user, InvoiceID: inv, PassID: pass, Kind: kind}, true
}

// processMessage queues one document's e-mail; an error (the queue write
// failed) is retried before the offset is committed.
func (c *PremiumInvoiceConsumer) processMessage(ctx context.Context, m kafka.Message) error {
	var env events.EventEnvelope
	if err := json.Unmarshal(m.Value, &env); err != nil {
		slog.Warn("premium invoice consumer: undecodable message skipped", "partition", m.Partition, "offset", m.Offset)
		return nil
	}
	ev, ok := planPremiumInvoiceEmail(env)
	if !ok {
		if service.PremiumKindFor(env.EventType) != "" {
			slog.Warn("premium invoice consumer: document event names no buyer or document; skipped",
				"event", env.EventType, "event_id", env.EventID, "partition", m.Partition, "offset", m.Offset)
		}
		return nil
	}
	if c.mail == nil {
		return nil
	}
	return c.mail.QueuePremiumInvoiceEmail(ctx, ev)
}
