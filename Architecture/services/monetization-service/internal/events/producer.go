package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sharedEvents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const TopicMonetization = "monetization.events"

// Event type constants for monetization domain.
const (
	EventSubscriptionCreated  = "subscription.created"
	EventSubscriptionCancelled = "subscription.cancelled"
	EventSubscriptionRenewed  = "subscription.renewed"
	EventSubscriptionExpired  = "subscription.expired"
	EventPayoutRequested      = "payout.requested"
	EventPayoutProcessed      = "payout.processed"
	EventDonationReceived     = "donation.received"
	EventAffiliateConversion  = "affiliate.conversion"
	EventWalletCredited       = "wallet.credited"
	EventSubscriptionPaused   = "subscription.paused"
	EventSubscriptionResumed  = "subscription.resumed"
	EventSubscriptionGrace    = "subscription.grace_started"
	EventSubscriptionUpgraded = "subscription.upgraded"
	EventEntitlementChanged   = "entitlement.changed"
)

// Producer publishes monetization events to Kafka.
type Producer struct {
	writer *kafka.Writer
}

// NewProducer creates a new Kafka producer writing to the given brokers and topic.
func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

// NewProducerWithDialer creates a new Kafka producer with an explicit dialer.
func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w}
}

// Close closes the underlying Kafka writer.
func (p *Producer) Close() error {
	return p.writer.Close()
}

// ---------------------------------------------------------------------------
// Subscription events
// ---------------------------------------------------------------------------

type SubscriptionCreatedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	TierID         string    `json:"tier_id"`
	TierName       string    `json:"tier_name"`
	PricePaise     int64     `json:"price_paise"`
	Currency       string    `json:"currency"`
	PeriodEnd      time.Time `json:"period_end"`
	CreatedAt      time.Time `json:"created_at"`
}

type SubscriptionCancelledPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	CancelledAt    time.Time `json:"cancelled_at"`
}

type SubscriptionRenewedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	NewPeriodEnd   time.Time `json:"new_period_end"`
	AmountPaise    int64     `json:"amount_paise"`
	Currency       string    `json:"currency"`
	RenewedAt      time.Time `json:"renewed_at"`
}

type SubscriptionExpiredPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	ExpiredAt      time.Time `json:"expired_at"`
	Reason         string    `json:"reason"` // "payment_failed" or "period_ended"
}

// PublishSubscriptionCreated publishes a subscription.created event.
func (p *Producer) PublishSubscriptionCreated(ctx context.Context, subscriptionID, subscriberID, creatorID, tierID uuid.UUID, tierName string, pricePaise int64, currency string, periodEnd time.Time) error {
	payload := SubscriptionCreatedPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		TierID:         tierID.String(),
		TierName:       tierName,
		PricePaise:     pricePaise,
		Currency:       currency,
		PeriodEnd:      periodEnd,
		CreatedAt:      time.Now(),
	}
	s := subscriberID.String()
	return p.publish(ctx, EventSubscriptionCreated, &s, payload)
}

// PublishSubscriptionCancelled publishes a subscription.cancelled event.
func (p *Producer) PublishSubscriptionCancelled(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID) error {
	payload := SubscriptionCancelledPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		CancelledAt:    time.Now(),
	}
	s := subscriberID.String()
	return p.publish(ctx, EventSubscriptionCancelled, &s, payload)
}

// PublishSubscriptionRenewed publishes a subscription.renewed event.
func (p *Producer) PublishSubscriptionRenewed(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, newPeriodEnd time.Time, amountPaise int64, currency string) error {
	payload := SubscriptionRenewedPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		NewPeriodEnd:   newPeriodEnd,
		AmountPaise:    amountPaise,
		Currency:       currency,
		RenewedAt:      time.Now(),
	}
	s := subscriberID.String()
	return p.publish(ctx, EventSubscriptionRenewed, &s, payload)
}

// PublishSubscriptionExpired publishes a subscription.expired event.
func (p *Producer) PublishSubscriptionExpired(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, reason string) error {
	payload := SubscriptionExpiredPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		ExpiredAt:      time.Now(),
		Reason:         reason,
	}
	s := subscriberID.String()
	return p.publish(ctx, EventSubscriptionExpired, &s, payload)
}

// ---------------------------------------------------------------------------
// Payout events
// ---------------------------------------------------------------------------

type PayoutRequestedPayload struct {
	TransactionID  string    `json:"transaction_id"`
	UserID         string    `json:"user_id"`
	AmountPaise    int64     `json:"amount_paise"`
	Currency       string    `json:"currency"`
	PayoutMethodID string    `json:"payout_method_id"`
	RequestedAt    time.Time `json:"requested_at"`
}

type PayoutProcessedPayload struct {
	TransactionID string    `json:"transaction_id"`
	UserID        string    `json:"user_id"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	ProcessedAt   time.Time `json:"processed_at"`
}

// PublishPayoutRequested publishes a payout.requested event.
func (p *Producer) PublishPayoutRequested(ctx context.Context, transactionID, userID uuid.UUID, amountPaise int64, currency, payoutMethodID string) error {
	payload := PayoutRequestedPayload{
		TransactionID:  transactionID.String(),
		UserID:         userID.String(),
		AmountPaise:    amountPaise,
		Currency:       currency,
		PayoutMethodID: payoutMethodID,
		RequestedAt:    time.Now(),
	}
	s := userID.String()
	return p.publish(ctx, EventPayoutRequested, &s, payload)
}

// PublishPayoutProcessed publishes a payout.processed event.
func (p *Producer) PublishPayoutProcessed(ctx context.Context, transactionID, userID uuid.UUID, amountPaise int64, currency string) error {
	payload := PayoutProcessedPayload{
		TransactionID: transactionID.String(),
		UserID:        userID.String(),
		AmountPaise:   amountPaise,
		Currency:      currency,
		ProcessedAt:   time.Now(),
	}
	s := userID.String()
	return p.publish(ctx, EventPayoutProcessed, &s, payload)
}

// ---------------------------------------------------------------------------
// Donation event
// ---------------------------------------------------------------------------

type DonationReceivedPayload struct {
	DonationID   string    `json:"donation_id"`
	FundraiserID string    `json:"fundraiser_id"`
	DonorID      string    `json:"donor_id"`
	AmountPaise  int64     `json:"amount_paise"`
	Currency     string    `json:"currency"`
	ReceivedAt   time.Time `json:"received_at"`
}

// PublishDonationReceived publishes a donation.received event.
func (p *Producer) PublishDonationReceived(ctx context.Context, donationID, fundraiserID, donorID uuid.UUID, amountPaise int64, currency string) error {
	payload := DonationReceivedPayload{
		DonationID:   donationID.String(),
		FundraiserID: fundraiserID.String(),
		DonorID:      donorID.String(),
		AmountPaise:  amountPaise,
		Currency:     currency,
		ReceivedAt:   time.Now(),
	}
	s := donorID.String()
	return p.publish(ctx, EventDonationReceived, &s, payload)
}

// ---------------------------------------------------------------------------
// Affiliate conversion event
// ---------------------------------------------------------------------------

type AffiliateConversionPayload struct {
	ConversionID    string    `json:"conversion_id"`
	AffiliateID     string    `json:"affiliate_id"`
	OrderID         string    `json:"order_id"`
	BuyerID         string    `json:"buyer_id"`
	CommissionPaise int64     `json:"commission_paise"`
	ConvertedAt     time.Time `json:"converted_at"`
}

// PublishAffiliateConversion publishes an affiliate.conversion event.
func (p *Producer) PublishAffiliateConversion(ctx context.Context, conversionID, affiliateID, orderID, buyerID uuid.UUID, commissionPaise int64) error {
	payload := AffiliateConversionPayload{
		ConversionID:    conversionID.String(),
		AffiliateID:     affiliateID.String(),
		OrderID:         orderID.String(),
		BuyerID:         buyerID.String(),
		CommissionPaise: commissionPaise,
		ConvertedAt:   time.Now(),
	}
	s := buyerID.String()
	return p.publish(ctx, EventAffiliateConversion, &s, payload)
}

// ---------------------------------------------------------------------------
// Wallet credited event
// ---------------------------------------------------------------------------

type WalletCreditedPayload struct {
	TransactionID string    `json:"transaction_id"`
	UserID        string    `json:"user_id"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	Reason        string    `json:"reason"`
	CreditedAt    time.Time `json:"credited_at"`
}

// PublishWalletCredited publishes a wallet.credited event.
func (p *Producer) PublishWalletCredited(ctx context.Context, transactionID, userID uuid.UUID, amountPaise int64, currency, reason string) error {
	payload := WalletCreditedPayload{
		TransactionID: transactionID.String(),
		UserID:        userID.String(),
		AmountPaise:   amountPaise,
		Currency:      currency,
		Reason:        reason,
		CreditedAt:    time.Now(),
	}
	s := userID.String()
	return p.publish(ctx, EventWalletCredited, &s, payload)
}

// ---------------------------------------------------------------------------
// Subscription lifecycle events
// ---------------------------------------------------------------------------

type SubscriptionPausedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	PauseUntil     time.Time `json:"pause_until"`
	PausedAt       time.Time `json:"paused_at"`
}

type SubscriptionResumedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	ResumedAt      time.Time `json:"resumed_at"`
}

type SubscriptionGraceStartedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	GraceEnd       time.Time `json:"grace_end"`
	RetryCount     int       `json:"retry_count"`
	StartedAt      time.Time `json:"started_at"`
}

type SubscriptionUpgradedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	OldTierID      string    `json:"old_tier_id"`
	NewTierID      string    `json:"new_tier_id"`
	UpgradedAt     time.Time `json:"upgraded_at"`
}

type EntitlementChangedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	Action         string    `json:"action"` // "revoked", "granted", "upgraded"
	ChangedAt      time.Time `json:"changed_at"`
}

// PublishSubscriptionPaused publishes a subscription.paused event.
func (p *Producer) PublishSubscriptionPaused(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, pauseUntil time.Time) error {
	payload := SubscriptionPausedPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		PauseUntil:     pauseUntil,
		PausedAt:       time.Now(),
	}
	s := subscriberID.String()
	return p.publish(ctx, EventSubscriptionPaused, &s, payload)
}

// PublishSubscriptionResumed publishes a subscription.resumed event.
func (p *Producer) PublishSubscriptionResumed(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID) error {
	payload := SubscriptionResumedPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		ResumedAt:      time.Now(),
	}
	s := subscriberID.String()
	return p.publish(ctx, EventSubscriptionResumed, &s, payload)
}

// PublishSubscriptionGraceStarted publishes a subscription.grace_started event.
func (p *Producer) PublishSubscriptionGraceStarted(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, graceEnd time.Time, retryCount int) error {
	payload := SubscriptionGraceStartedPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		GraceEnd:       graceEnd,
		RetryCount:     retryCount,
		StartedAt:      time.Now(),
	}
	s := subscriberID.String()
	return p.publish(ctx, EventSubscriptionGrace, &s, payload)
}

// PublishSubscriptionUpgraded publishes a subscription.upgraded event.
func (p *Producer) PublishSubscriptionUpgraded(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, oldTierID, newTierID string) error {
	payload := SubscriptionUpgradedPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		OldTierID:      oldTierID,
		NewTierID:      newTierID,
		UpgradedAt:     time.Now(),
	}
	s := subscriberID.String()
	return p.publish(ctx, EventSubscriptionUpgraded, &s, payload)
}

// PublishEntitlementChanged publishes an entitlement.changed event for downstream services.
func (p *Producer) PublishEntitlementChanged(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, action string) error {
	payload := EntitlementChangedPayload{
		SubscriptionID: subscriptionID.String(),
		SubscriberID:   subscriberID.String(),
		CreatorID:      creatorID.String(),
		Action:         action,
		ChangedAt:      time.Now(),
	}
	s := subscriberID.String()
	return p.publish(ctx, EventEntitlementChanged, &s, payload)
}

// ---------------------------------------------------------------------------
// Internal publish helper
// ---------------------------------------------------------------------------

func (p *Producer) publish(ctx context.Context, eventType string, actorID *string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	envelope := sharedEvents.NewEnvelope(ctx, eventType, actorID, payloadBytes)

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: envelopeBytes,
	})
}


