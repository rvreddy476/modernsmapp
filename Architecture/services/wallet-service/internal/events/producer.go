// Package events wraps Kafka publishing for wallet-service.
//
// Every payment-touching state transition emits exactly one event. Downstream
// consumers (notification-service, analytics-service, monetization-service)
// listen on the wallet-events topic and switch on event_type. Events are
// audit-only — they never mutate the source-of-truth balances/transactions
// rows — so a duplicate publish has no destructive effect (idempotency on
// the consumer side handles dedup via Redis, see shared/kafka).
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

// --- Top-up ---------------------------------------------------------------

type TopUpStartedPayload struct {
	UserID        string    `json:"user_id"`
	TransactionID string    `json:"transaction_id"`
	AmountPaise   int64     `json:"amount_paise"`
	StartedAt     time.Time `json:"started_at"`
}

type TopUpSucceededPayload struct {
	UserID        string    `json:"user_id"`
	TransactionID string    `json:"transaction_id"`
	AmountPaise   int64     `json:"amount_paise"`
	BankTxnRef    string    `json:"bank_txn_ref,omitempty"`
	UPITxnRef     string    `json:"upi_txn_ref,omitempty"`
	SettledAt     time.Time `json:"settled_at"`
}

type TopUpFailedPayload struct {
	UserID        string    `json:"user_id"`
	TransactionID string    `json:"transaction_id"`
	AmountPaise   int64     `json:"amount_paise"`
	Reason        string    `json:"reason"`
	FailedAt      time.Time `json:"failed_at"`
}

func (p *Producer) PublishTopUpStarted(ctx context.Context, userID, txID uuid.UUID, amountPaise int64) error {
	return p.publish(ctx, events.EventWalletTopUpStarted, &userID, TopUpStartedPayload{
		UserID: userID.String(), TransactionID: txID.String(),
		AmountPaise: amountPaise, StartedAt: time.Now(),
	})
}

func (p *Producer) PublishTopUpSucceeded(ctx context.Context, userID, txID uuid.UUID, amountPaise int64, bankRef, upiRef string) error {
	return p.publish(ctx, events.EventWalletTopUpSucceeded, &userID, TopUpSucceededPayload{
		UserID: userID.String(), TransactionID: txID.String(),
		AmountPaise: amountPaise, BankTxnRef: bankRef, UPITxnRef: upiRef,
		SettledAt: time.Now(),
	})
}

func (p *Producer) PublishTopUpFailed(ctx context.Context, userID, txID uuid.UUID, amountPaise int64, reason string) error {
	return p.publish(ctx, events.EventWalletTopUpFailed, &userID, TopUpFailedPayload{
		UserID: userID.String(), TransactionID: txID.String(),
		AmountPaise: amountPaise, Reason: reason, FailedAt: time.Now(),
	})
}

// --- Send ----------------------------------------------------------------

type SendStartedPayload struct {
	SenderID          string    `json:"sender_id"`
	TransactionID     string    `json:"transaction_id"`
	AmountPaise       int64     `json:"amount_paise"`
	RecipientUserID   string    `json:"recipient_user_id,omitempty"`
	RecipientPhone    string    `json:"recipient_phone,omitempty"`
	StartedAt         time.Time `json:"started_at"`
}

type SendSucceededPayload struct {
	SenderID          string    `json:"sender_id"`
	TransactionID     string    `json:"transaction_id"`
	AmountPaise       int64     `json:"amount_paise"`
	RecipientUserID   string    `json:"recipient_user_id,omitempty"`
	RecipientPhone    string    `json:"recipient_phone,omitempty"`
	BankTxnRef        string    `json:"bank_txn_ref,omitempty"`
	SettledAt         time.Time `json:"settled_at"`
}

type SendFailedPayload struct {
	SenderID      string    `json:"sender_id"`
	TransactionID string    `json:"transaction_id"`
	AmountPaise   int64     `json:"amount_paise"`
	Reason        string    `json:"reason"`
	FailedAt      time.Time `json:"failed_at"`
}

type ReceiveCreditedPayload struct {
	RecipientID   string    `json:"recipient_id"`
	SenderID      string    `json:"sender_id"`
	TransactionID string    `json:"transaction_id"`
	AmountPaise   int64     `json:"amount_paise"`
	CreditedAt    time.Time `json:"credited_at"`
}

func (p *Producer) PublishSendStarted(ctx context.Context, senderID, txID uuid.UUID, amountPaise int64, recipientUserID *uuid.UUID, recipientPhone string) error {
	payload := SendStartedPayload{
		SenderID: senderID.String(), TransactionID: txID.String(),
		AmountPaise: amountPaise, RecipientPhone: recipientPhone, StartedAt: time.Now(),
	}
	if recipientUserID != nil {
		payload.RecipientUserID = recipientUserID.String()
	}
	return p.publish(ctx, events.EventWalletSendStarted, &senderID, payload)
}

func (p *Producer) PublishSendSucceeded(ctx context.Context, senderID, txID uuid.UUID, amountPaise int64, recipientUserID *uuid.UUID, recipientPhone, bankRef string) error {
	payload := SendSucceededPayload{
		SenderID: senderID.String(), TransactionID: txID.String(),
		AmountPaise: amountPaise, RecipientPhone: recipientPhone,
		BankTxnRef: bankRef, SettledAt: time.Now(),
	}
	if recipientUserID != nil {
		payload.RecipientUserID = recipientUserID.String()
	}
	return p.publish(ctx, events.EventWalletSendSucceeded, &senderID, payload)
}

func (p *Producer) PublishSendFailed(ctx context.Context, senderID, txID uuid.UUID, amountPaise int64, reason string) error {
	return p.publish(ctx, events.EventWalletSendFailed, &senderID, SendFailedPayload{
		SenderID: senderID.String(), TransactionID: txID.String(),
		AmountPaise: amountPaise, Reason: reason, FailedAt: time.Now(),
	})
}

func (p *Producer) PublishReceiveCredited(ctx context.Context, recipientID, senderID, txID uuid.UUID, amountPaise int64) error {
	return p.publish(ctx, events.EventWalletReceiveCredited, &recipientID, ReceiveCreditedPayload{
		RecipientID: recipientID.String(), SenderID: senderID.String(),
		TransactionID: txID.String(), AmountPaise: amountPaise, CreditedAt: time.Now(),
	})
}

// --- Merchant pay --------------------------------------------------------

type MerchantDebitedPayload struct {
	UserID          string    `json:"user_id"`
	TransactionID   string    `json:"transaction_id"`
	AmountPaise     int64     `json:"amount_paise"`
	MerchantService string    `json:"merchant_service"`
	MerchantRef     string    `json:"merchant_ref"`
	DebitedAt       time.Time `json:"debited_at"`
}

func (p *Producer) PublishMerchantDebited(ctx context.Context, userID, txID uuid.UUID, amountPaise int64, merchantService, merchantRef string) error {
	return p.publish(ctx, events.EventWalletMerchantDebited, &userID, MerchantDebitedPayload{
		UserID: userID.String(), TransactionID: txID.String(),
		AmountPaise: amountPaise, MerchantService: merchantService,
		MerchantRef: merchantRef, DebitedAt: time.Now(),
	})
}

// --- Refund --------------------------------------------------------------

type RefundIssuedPayload struct {
	UserID                string    `json:"user_id"`
	TransactionID         string    `json:"transaction_id"`
	OriginalTransactionID string    `json:"original_transaction_id"`
	AmountPaise           int64     `json:"amount_paise"`
	Reason                string    `json:"reason,omitempty"`
	IssuedAt              time.Time `json:"issued_at"`
}

func (p *Producer) PublishRefundIssued(ctx context.Context, userID, txID, originalID uuid.UUID, amountPaise int64, reason string) error {
	return p.publish(ctx, events.EventWalletRefundIssued, &userID, RefundIssuedPayload{
		UserID: userID.String(), TransactionID: txID.String(),
		OriginalTransactionID: originalID.String(),
		AmountPaise:           amountPaise, Reason: reason, IssuedAt: time.Now(),
	})
}

// --- KYC -----------------------------------------------------------------

type KYCCompletedPayload struct {
	UserID      string    `json:"user_id"`
	NewTier     string    `json:"new_tier"`
	CompletedAt time.Time `json:"completed_at"`
}

func (p *Producer) PublishKYCCompleted(ctx context.Context, userID uuid.UUID, newTier string) error {
	return p.publish(ctx, events.EventWalletKYCCompleted, &userID, KYCCompletedPayload{
		UserID: userID.String(), NewTier: newTier, CompletedAt: time.Now(),
	})
}

// --- Freeze / Unfreeze ----------------------------------------------------

type FreezePayload struct {
	UserID    string    `json:"user_id"`
	Reason    string    `json:"reason,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (p *Producer) PublishFrozen(ctx context.Context, userID uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventWalletFrozen, &userID, FreezePayload{
		UserID: userID.String(), Reason: reason, OccurredAt: time.Now(),
	})
}

func (p *Producer) PublishUnfrozen(ctx context.Context, userID uuid.UUID) error {
	return p.publish(ctx, events.EventWalletUnfrozen, &userID, FreezePayload{
		UserID: userID.String(), OccurredAt: time.Now(),
	})
}

// --- internal -------------------------------------------------------------

// publish marshals payload, wraps in shared envelope and writes to Kafka.
// If the writer is nil (tests), it is a no-op.
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
