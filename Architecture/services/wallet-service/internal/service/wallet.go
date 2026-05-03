// Package service holds the business-logic layer for wallet-service.
//
// The Service struct is the single composition root: it depends on the store
// (Postgres mirror), the bank client (partner-bank PPI), and the events
// producer. Sub-files split methods by aggregate (balance, topup, send,
// history, kyc) so each surface can be reasoned about in isolation while
// sharing the same struct.
//
// BC-of-PPI MODEL: the Service NEVER moves "money" by itself. It moves
// balance-mirror rows in our DB and asks the bank client to move actual funds
// at the partner bank. Every payment-touching path takes an idempotency key.
package service

import (
	"context"

	"github.com/atpost/wallet-service/internal/bank"
	"github.com/atpost/wallet-service/internal/store"
	"github.com/google/uuid"
)

// Service is the wallet-service business-logic layer.
type Service struct {
	store    *store.Store
	bank     bank.BankClient
	producer EventPublisher
	cfg      Config
}

// Config tunes service-level constants. Defaults match v1 spec.
type Config struct {
	// PartnerBankVPA is the destination VPA used in UPI Intent URLs the
	// client opens to launch a top-up. e.g. "atpostwallet@partnerbank".
	PartnerBankVPA string
	// AppDisplayName surfaces in UPI Intent (`pn=` parameter).
	AppDisplayName string
	// PoolBankRef is OUR pool sub-account at the partner bank — the
	// destination for incoming UPI top-ups before they are forwarded into
	// the user's PPI sub-account.
	PoolBankRef string
	// TopUpExpiry is how long a pending top-up may sit before the expirer
	// flips it to failed and refunds pending_in_paise.
	TopUpExpirySeconds int
}

// EventPublisher is the subset of *events.Producer the service needs. Stays
// an interface so unit tests can inject a no-op implementation.
type EventPublisher interface {
	PublishTopUpStarted(ctx context.Context, userID, txID uuid.UUID, amountPaise int64) error
	PublishTopUpSucceeded(ctx context.Context, userID, txID uuid.UUID, amountPaise int64, bankRef, upiRef string) error
	PublishTopUpFailed(ctx context.Context, userID, txID uuid.UUID, amountPaise int64, reason string) error
	PublishSendStarted(ctx context.Context, senderID, txID uuid.UUID, amountPaise int64, recipientUserID *uuid.UUID, recipientPhone string) error
	PublishSendSucceeded(ctx context.Context, senderID, txID uuid.UUID, amountPaise int64, recipientUserID *uuid.UUID, recipientPhone, bankRef string) error
	PublishSendFailed(ctx context.Context, senderID, txID uuid.UUID, amountPaise int64, reason string) error
	PublishReceiveCredited(ctx context.Context, recipientID, senderID, txID uuid.UUID, amountPaise int64) error
	PublishMerchantDebited(ctx context.Context, userID, txID uuid.UUID, amountPaise int64, merchantService, merchantRef string) error
	PublishRefundIssued(ctx context.Context, userID, txID, originalID uuid.UUID, amountPaise int64, reason string) error
	PublishKYCCompleted(ctx context.Context, userID uuid.UUID, newTier string) error
	PublishFrozen(ctx context.Context, userID uuid.UUID, reason string) error
	PublishUnfrozen(ctx context.Context, userID uuid.UUID) error
}

// noopPublisher is the default. Replaced via SetProducer in main.go.
type noopPublisher struct{}

func (noopPublisher) PublishTopUpStarted(_ context.Context, _, _ uuid.UUID, _ int64) error {
	return nil
}
func (noopPublisher) PublishTopUpSucceeded(_ context.Context, _, _ uuid.UUID, _ int64, _, _ string) error {
	return nil
}
func (noopPublisher) PublishTopUpFailed(_ context.Context, _, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (noopPublisher) PublishSendStarted(_ context.Context, _, _ uuid.UUID, _ int64, _ *uuid.UUID, _ string) error {
	return nil
}
func (noopPublisher) PublishSendSucceeded(_ context.Context, _, _ uuid.UUID, _ int64, _ *uuid.UUID, _, _ string) error {
	return nil
}
func (noopPublisher) PublishSendFailed(_ context.Context, _, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (noopPublisher) PublishReceiveCredited(_ context.Context, _, _, _ uuid.UUID, _ int64) error {
	return nil
}
func (noopPublisher) PublishMerchantDebited(_ context.Context, _, _ uuid.UUID, _ int64, _, _ string) error {
	return nil
}
func (noopPublisher) PublishRefundIssued(_ context.Context, _, _, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (noopPublisher) PublishKYCCompleted(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (noopPublisher) PublishFrozen(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (noopPublisher) PublishUnfrozen(_ context.Context, _ uuid.UUID) error         { return nil }

// New returns a Service wired to the given store and bank client. Producer
// defaults to a no-op; main.go calls SetProducer with the real Kafka
// publisher.
func New(s *store.Store, b bank.BankClient, cfg Config) *Service {
	if cfg.AppDisplayName == "" {
		cfg.AppDisplayName = "AtPost Wallet"
	}
	if cfg.PartnerBankVPA == "" {
		cfg.PartnerBankVPA = "atpostwallet@partnerbank"
	}
	if cfg.TopUpExpirySeconds <= 0 {
		cfg.TopUpExpirySeconds = 1800 // 30 minutes
	}
	return &Service{
		store:    s,
		bank:     b,
		producer: noopPublisher{},
		cfg:      cfg,
	}
}

// SetProducer swaps in a real event publisher. Called from main.go after the
// Kafka writer is initialised.
func (s *Service) SetProducer(p EventPublisher) { s.producer = p }

// Config returns a copy of the active config — handy in tests.
func (s *Service) Cfg() Config { return s.cfg }

// Store returns the underlying store. Exposed so handlers can issue thin
// read-only fan-outs (recipients, single-tx detail) without forcing every
// query through the service layer.
func (s *Service) Store() *store.Store { return s.store }
