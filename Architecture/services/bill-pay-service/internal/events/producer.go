// Package events wraps Kafka publishing for bill-pay-service.
//
// Every payment-touching state transition emits exactly one event. Downstream
// consumers (notification-service, analytics-service) listen on the
// billpay-events topic and switch on event_type. Events are audit-only —
// they never mutate the source-of-truth payments rows — so a duplicate
// publish has no destructive effect.
//
// DPDP NOTE: payloads MUST NOT carry full account identifiers (consumer
// numbers, mobile numbers). Use the masked form via store.MaskIdentifier
// when an identifier discriminator is necessary.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Producer is a thin wrapper around kafka.Writer with typed Publish helpers.
type Producer struct {
	writer *kafka.Writer
}

// NewProducer returns a Producer using the default dialer.
func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

// NewProducerWithDialer is the constructor used by main.go so TLS / SASL
// configuration from transport.KafkaDialerFromEnv flows through.
func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w}
}

// Close flushes and closes the underlying writer.
func (p *Producer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}

// --- Payloads -------------------------------------------------------------

type PaymentInitiatedPayload struct {
	UserID      string    `json:"user_id"`
	PaymentID   string    `json:"payment_id"`
	ProviderID  string    `json:"provider_id"`
	AmountPaise int64     `json:"amount_paise"`
	Method      string    `json:"payment_method"`
	StartedAt   time.Time `json:"started_at"`
}

type PaymentSucceededPayload struct {
	UserID         string    `json:"user_id"`
	PaymentID      string    `json:"payment_id"`
	ProviderID     string    `json:"provider_id"`
	AmountPaise    int64     `json:"amount_paise"`
	ReceiptNumber  string    `json:"receipt_number,omitempty"`
	SetuPaymentRef string    `json:"setu_payment_ref,omitempty"`
	SettledAt      time.Time `json:"settled_at"`
}

type PaymentFailedPayload struct {
	UserID      string    `json:"user_id"`
	PaymentID   string    `json:"payment_id"`
	ProviderID  string    `json:"provider_id"`
	AmountPaise int64     `json:"amount_paise"`
	Reason      string    `json:"reason"`
	FailedAt    time.Time `json:"failed_at"`
}

type PaymentRefundedPayload struct {
	UserID      string    `json:"user_id"`
	PaymentID   string    `json:"payment_id"`
	AmountPaise int64     `json:"amount_paise"`
	Reason      string    `json:"reason,omitempty"`
	RefundedAt  time.Time `json:"refunded_at"`
}

type BillFetchedPayload struct {
	UserID      string    `json:"user_id"`
	AccountID   string    `json:"account_id"`
	BillID      string    `json:"bill_id"`
	AmountPaise int64     `json:"amount_paise"`
	DueDate     string    `json:"due_date,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
}

type BillDueSoonPayload struct {
	UserID      string    `json:"user_id"`
	AccountID   string    `json:"account_id"`
	BillID      string    `json:"bill_id"`
	DueDate     string    `json:"due_date"`
	AmountPaise int64     `json:"amount_paise"`
	Channels    []string  `json:"channels"`
	NotifiedAt  time.Time `json:"notified_at"`
}

type ScheduledExecutedPayload struct {
	UserID      string    `json:"user_id"`
	ScheduledID string    `json:"scheduled_id"`
	PaymentID   string    `json:"payment_id"`
	AmountPaise int64     `json:"amount_paise"`
	ExecutedAt  time.Time `json:"executed_at"`
}

type ScheduledFailedPayload struct {
	UserID      string    `json:"user_id"`
	ScheduledID string    `json:"scheduled_id"`
	Reason      string    `json:"reason"`
	FailedAt    time.Time `json:"failed_at"`
}

type AccountAddedPayload struct {
	UserID     string    `json:"user_id"`
	AccountID  string    `json:"account_id"`
	ProviderID string    `json:"provider_id"`
	AddedAt    time.Time `json:"added_at"`
}

type AccountRemovedPayload struct {
	UserID    string    `json:"user_id"`
	AccountID string    `json:"account_id"`
	RemovedAt time.Time `json:"removed_at"`
}

// --- Publishers -----------------------------------------------------------

func (p *Producer) PublishPaymentInitiated(ctx context.Context, userID, paymentID, providerID uuid.UUID, amountPaise int64, method string) error {
	return p.publish(ctx, events.EventBillPayPaymentInitiated, &userID, PaymentInitiatedPayload{
		UserID: userID.String(), PaymentID: paymentID.String(),
		ProviderID: providerID.String(), AmountPaise: amountPaise,
		Method: method, StartedAt: time.Now(),
	})
}

func (p *Producer) PublishPaymentSucceeded(ctx context.Context, userID, paymentID, providerID uuid.UUID, amountPaise int64, receiptNumber, setuRef string) error {
	return p.publish(ctx, events.EventBillPayPaymentSucceeded, &userID, PaymentSucceededPayload{
		UserID: userID.String(), PaymentID: paymentID.String(),
		ProviderID: providerID.String(), AmountPaise: amountPaise,
		ReceiptNumber: receiptNumber, SetuPaymentRef: setuRef,
		SettledAt: time.Now(),
	})
}

func (p *Producer) PublishPaymentFailed(ctx context.Context, userID, paymentID, providerID uuid.UUID, amountPaise int64, reason string) error {
	return p.publish(ctx, events.EventBillPayPaymentFailed, &userID, PaymentFailedPayload{
		UserID: userID.String(), PaymentID: paymentID.String(),
		ProviderID: providerID.String(), AmountPaise: amountPaise,
		Reason: reason, FailedAt: time.Now(),
	})
}

func (p *Producer) PublishPaymentRefunded(ctx context.Context, userID, paymentID uuid.UUID, amountPaise int64, reason string) error {
	return p.publish(ctx, events.EventBillPayPaymentRefunded, &userID, PaymentRefundedPayload{
		UserID: userID.String(), PaymentID: paymentID.String(),
		AmountPaise: amountPaise, Reason: reason, RefundedAt: time.Now(),
	})
}

func (p *Producer) PublishBillFetched(ctx context.Context, userID, accountID, billID uuid.UUID, amountPaise int64, dueDate string) error {
	return p.publish(ctx, events.EventBillPayBillFetched, &userID, BillFetchedPayload{
		UserID: userID.String(), AccountID: accountID.String(), BillID: billID.String(),
		AmountPaise: amountPaise, DueDate: dueDate, FetchedAt: time.Now(),
	})
}

func (p *Producer) PublishBillDueSoon(ctx context.Context, userID, accountID, billID uuid.UUID, dueDate string, amountPaise int64, channels []string) error {
	return p.publish(ctx, events.EventBillPayBillDueSoon, &userID, BillDueSoonPayload{
		UserID: userID.String(), AccountID: accountID.String(), BillID: billID.String(),
		DueDate: dueDate, AmountPaise: amountPaise, Channels: channels, NotifiedAt: time.Now(),
	})
}

func (p *Producer) PublishScheduledExecuted(ctx context.Context, userID, scheduledID, paymentID uuid.UUID, amountPaise int64) error {
	return p.publish(ctx, events.EventBillPayScheduledExecuted, &userID, ScheduledExecutedPayload{
		UserID: userID.String(), ScheduledID: scheduledID.String(),
		PaymentID: paymentID.String(), AmountPaise: amountPaise,
		ExecutedAt: time.Now(),
	})
}

func (p *Producer) PublishScheduledFailed(ctx context.Context, userID, scheduledID uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventBillPayScheduledFailed, &userID, ScheduledFailedPayload{
		UserID: userID.String(), ScheduledID: scheduledID.String(),
		Reason: reason, FailedAt: time.Now(),
	})
}

func (p *Producer) PublishAccountAdded(ctx context.Context, userID, accountID, providerID uuid.UUID) error {
	return p.publish(ctx, events.EventBillPayAccountAdded, &userID, AccountAddedPayload{
		UserID: userID.String(), AccountID: accountID.String(),
		ProviderID: providerID.String(), AddedAt: time.Now(),
	})
}

func (p *Producer) PublishAccountRemoved(ctx context.Context, userID, accountID uuid.UUID) error {
	return p.publish(ctx, events.EventBillPayAccountRemoved, &userID, AccountRemovedPayload{
		UserID: userID.String(), AccountID: accountID.String(),
		RemovedAt: time.Now(),
	})
}

// publish marshals payload, wraps in shared envelope and writes to Kafka.
// nil writer = no-op (tests).
func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload any) error {
	if p == nil || p.writer == nil {
		return nil
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	var actorStr *string
	if actorID != nil {
		s := actorID.String()
		actorStr = &s
	}
	envelope := events.NewEnvelope(ctx, eventType, actorStr, payloadBytes)
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: envelopeBytes,
	})
}
