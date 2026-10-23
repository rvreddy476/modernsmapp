package events

// Paid memberships (phase 1 payments contract, "Events", 2026-10-23).
//
// monetization-service publishes on monetization.events: the shared JSON
// envelope (events.EventEnvelope), Kafka key = event id. This consumer has its
// own group, reads only the five membership.* types and ignores the rest of
// the topic (entitlement.changed, subscription.*, creator_ledger.*, ...).
// planMembershipNotices is pure; delivery is the service's
// CreateMembershipNotification (service/membership_notify.go).
//
// Offsets: a message is committed once every notice it names is delivered
// (processAndCommit retries a delivery error; the per-recipient identity makes
// the retry a no-op for recipients already told). A message that cannot be
// decoded, or names no deliverable recipient, is logged and committed: it
// would fail the same way forever, and must not stall the partition.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// MonetizationTopic is monetization-service's TopicMonetization.
const MonetizationTopic = "monetization.events"

// The membership event types (contract "Events").
const (
	EventMembershipStarted       = "membership.started"
	EventMembershipRenewed       = "membership.renewed"
	EventMembershipRenewalDue    = "membership.renewal_due"
	EventMembershipPaymentFailed = "membership.payment_failed"
	EventMembershipEnded         = "membership.ended"
)

// membershipPayload is the contract's common payload. period_end is RFC3339;
// it is read as a string so a missing or odd value never fails the decode.
type membershipPayload struct {
	SubscriptionID string `json:"subscription_id"`
	SubscriberID   string `json:"subscriber_id"`
	CreatorID      string `json:"creator_id"`
	TierID         string `json:"tier_id"`
	TierName       string `json:"tier_name"`
	AmountPaise    int64  `json:"amount_paise"`
	Currency       string `json:"currency"`
	PeriodEnd      string `json:"period_end"`
	// DaysLeft is set on membership.renewal_due (3 or 0).
	DaysLeft *int `json:"days_left,omitempty"`
}

// errMembershipPermanent marks a message that will never deliver: it is
// logged and committed, never retried.
var errMembershipPermanent = errors.New("membership event undeliverable")

// membershipNotifier is the service seam; *service.Service satisfies it.
type membershipNotifier interface {
	CreateMembershipNotification(ctx context.Context, n service.MembershipNotification) error
}

// membershipReader is the slice of *kafka.Reader the consumer uses.
type membershipReader interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

// MembershipConsumer reads monetization.events for membership notices.
type MembershipConsumer struct {
	reader membershipReader
	notify membershipNotifier
}

// NewMembershipConsumerWithDialer builds the consumer on its own group.
func NewMembershipConsumerWithDialer(brokers []string, groupID, topic string, svc *service.Service, dialer *kafka.Dialer) *MembershipConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	c := &MembershipConsumer{reader: reader}
	if svc != nil { // never store a typed nil in the interface
		c.notify = svc
	}
	return c
}

// Start reads until ctx ends. Messages are handled in order, one at a time:
// membership traffic is a few events per member per month.
func (c *MembershipConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("membership consumer shutting down")
			} else {
				slog.Error("membership consumer fetch failed", "error", err)
			}
			return
		}
		if !processAndCommit(ctx, c.reader, m, c.processMessage) {
			return
		}
	}
}

// Close closes the reader.
func (c *MembershipConsumer) Close() error { return c.reader.Close() }

// processMessage delivers one message's notices. It returns an error only for
// a delivery failure worth retrying; a permanent failure is logged and nil.
func (c *MembershipConsumer) processMessage(ctx context.Context, m kafka.Message) error {
	var env events.EventEnvelope
	if err := json.Unmarshal(m.Value, &env); err != nil {
		slog.Warn("membership consumer: undecodable message skipped",
			"partition", m.Partition, "offset", m.Offset, "error", err)
		return nil
	}
	notices, err := planMembershipNotices(env, membershipDedupID(env, m), time.Now())
	if err != nil {
		slog.Warn("membership consumer: event skipped",
			"event", env.EventType, "event_id", env.EventID, "partition", m.Partition, "offset", m.Offset, "error", err)
		return nil
	}
	if c.notify == nil {
		return nil
	}
	for _, n := range notices {
		if err := c.notify.CreateMembershipNotification(ctx, n); err != nil {
			return fmt.Errorf("%s to %s: %w", n.Type, n.RecipientID, err)
		}
	}
	return nil
}

// membershipDedupID is the event's own id; for an envelope without one, the
// Kafka key (the producer keys by event id), then a hash of the message, so a
// redelivery still dedups.
func membershipDedupID(env events.EventEnvelope, m kafka.Message) string {
	if id := strings.TrimSpace(env.EventID); id != "" {
		return id
	}
	if k := strings.TrimSpace(string(m.Key)); k != "" {
		return k
	}
	sum := sha256.Sum256(m.Value)
	return "sha256:" + hex.EncodeToString(sum[:16])
}

// planMembershipNotices turns one envelope into the notices it delivers. An
// event type outside the five is ignored (nil, nil). A membership event whose
// payload does not decode, or names no valid subscription, member or creator,
// is an errMembershipPermanent.
func planMembershipNotices(env events.EventEnvelope, dedupID string, now time.Time) ([]service.MembershipNotification, error) {
	switch env.EventType {
	case EventMembershipStarted, EventMembershipRenewed, EventMembershipRenewalDue,
		EventMembershipPaymentFailed, EventMembershipEnded:
	default:
		return nil, nil
	}
	var p membershipPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", errMembershipPermanent, err)
	}
	sub, okSub := parseMembershipID(p.SubscriptionID)
	member, okMember := parseMembershipID(p.SubscriberID)
	creator, okCreator := parseMembershipID(p.CreatorID)
	if !okSub || !okMember || !okCreator {
		return nil, fmt.Errorf("%w: subscription, subscriber or creator id missing", errMembershipPermanent)
	}
	if member == creator {
		return nil, fmt.Errorf("%w: subscriber is the creator", errMembershipPermanent)
	}
	periodEnd, _ := time.Parse(time.RFC3339, strings.TrimSpace(p.PeriodEnd))
	daysLeft := service.MembershipDaysLeftUnknown
	if p.DaysLeft != nil && *p.DaysLeft >= 0 {
		daysLeft = *p.DaysLeft
	}
	createdAt := env.OccurredAt
	if createdAt.IsZero() {
		createdAt = now
	}
	toMember := func(typ string) service.MembershipNotification {
		return service.MembershipNotification{
			EventID: dedupID, RecipientID: member, ActorID: creator, Type: typ,
			SubscriptionID: sub, TierName: p.TierName, PeriodEnd: periodEnd, DaysLeft: daysLeft,
			DeepLink: service.MembershipDeepLink(sub), CreatedAt: createdAt,
		}
	}
	switch env.EventType {
	case EventMembershipStarted:
		toCreator := toMember(service.MembershipTypeNewMember)
		toCreator.RecipientID, toCreator.ActorID = creator, member
		toCreator.DeepLink = service.MembershipCreatorDeepLink
		return []service.MembershipNotification{toCreator, toMember(service.MembershipTypeJoined)}, nil
	case EventMembershipRenewed:
		return []service.MembershipNotification{toMember(service.MembershipTypeRenewed)}, nil
	case EventMembershipRenewalDue:
		return []service.MembershipNotification{toMember(service.MembershipTypeRenewalDue)}, nil
	case EventMembershipPaymentFailed:
		return []service.MembershipNotification{toMember(service.MembershipTypePaymentFailed)}, nil
	default: // EventMembershipEnded
		return []service.MembershipNotification{toMember(service.MembershipTypeEnded)}, nil
	}
}

func parseMembershipID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	return id, err == nil && id != uuid.Nil
}
