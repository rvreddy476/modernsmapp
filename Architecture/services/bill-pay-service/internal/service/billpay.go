// Package service holds the business-logic layer for bill-pay-service.
//
// The Service struct is the single composition root: it depends on the store,
// the Setu client (BBPS aggregator), the wallet client (HTTP -> wallet-service),
// and the events producer. Sub-files split methods by aggregate (providers,
// accounts, pay, recharge, reminders, scheduled, payments).
//
// PHASE 2 D2: Setu is the bill-network rail; AtPost is the consumer-facing
// biller. Every payment-touching path is idempotent on a request-supplied
// idempotency_key.
package service

import (
	"context"
	"encoding/json"

	"github.com/atpost/bill-pay-service/internal/setu"
	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/atpost/bill-pay-service/internal/wallet"
	"github.com/google/uuid"
)

// marshalCustomerParams serialises the Setu biller's customer-param specs to
// the JSONB shape we persist on billpay.providers.customer_params.
func marshalCustomerParams(specs []setu.CustomerParamSpec) ([]byte, error) {
	if len(specs) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(specs)
}

// Service is the bill-pay-service business-logic layer.
type Service struct {
	store    *store.Store
	setu     setu.SetuClient
	wallet   wallet.WalletClient
	producer EventPublisher
	cfg      Config
}

// Config tunes service-level constants.
type Config struct {
	// DefaultMobileCircle is the fallback circle when DetectOperatorCircle
	// returns empty. Bengaluru launch -> "KA".
	DefaultMobileCircle string
}

// EventPublisher is the subset of *events.Producer the service needs. Stays
// an interface so unit tests can inject a no-op implementation.
type EventPublisher interface {
	PublishPaymentInitiated(ctx context.Context, userID, paymentID, providerID uuid.UUID, amountPaise int64, method string) error
	PublishPaymentSucceeded(ctx context.Context, userID, paymentID, providerID uuid.UUID, amountPaise int64, receiptNumber, setuRef string) error
	PublishPaymentFailed(ctx context.Context, userID, paymentID, providerID uuid.UUID, amountPaise int64, reason string) error
	PublishPaymentRefunded(ctx context.Context, userID, paymentID uuid.UUID, amountPaise int64, reason string) error
	PublishBillFetched(ctx context.Context, userID, accountID, billID uuid.UUID, amountPaise int64, dueDate string) error
	PublishBillDueSoon(ctx context.Context, userID, accountID, billID uuid.UUID, dueDate string, amountPaise int64, channels []string) error
	PublishScheduledExecuted(ctx context.Context, userID, scheduledID, paymentID uuid.UUID, amountPaise int64) error
	PublishScheduledFailed(ctx context.Context, userID, scheduledID uuid.UUID, reason string) error
	PublishAccountAdded(ctx context.Context, userID, accountID, providerID uuid.UUID) error
	PublishAccountRemoved(ctx context.Context, userID, accountID uuid.UUID) error
}

// noopPublisher is the default; replaced via SetProducer.
type noopPublisher struct{}

func (noopPublisher) PublishPaymentInitiated(_ context.Context, _, _, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (noopPublisher) PublishPaymentSucceeded(_ context.Context, _, _, _ uuid.UUID, _ int64, _, _ string) error {
	return nil
}
func (noopPublisher) PublishPaymentFailed(_ context.Context, _, _, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (noopPublisher) PublishPaymentRefunded(_ context.Context, _, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (noopPublisher) PublishBillFetched(_ context.Context, _, _, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (noopPublisher) PublishBillDueSoon(_ context.Context, _, _, _ uuid.UUID, _ string, _ int64, _ []string) error {
	return nil
}
func (noopPublisher) PublishScheduledExecuted(_ context.Context, _, _, _ uuid.UUID, _ int64) error {
	return nil
}
func (noopPublisher) PublishScheduledFailed(_ context.Context, _, _ uuid.UUID, _ string) error {
	return nil
}
func (noopPublisher) PublishAccountAdded(_ context.Context, _, _, _ uuid.UUID) error { return nil }
func (noopPublisher) PublishAccountRemoved(_ context.Context, _, _ uuid.UUID) error  { return nil }

// New constructs a Service.
func New(s *store.Store, sc setu.SetuClient, wc wallet.WalletClient, cfg Config) *Service {
	if cfg.DefaultMobileCircle == "" {
		cfg.DefaultMobileCircle = "KA"
	}
	return &Service{
		store:    s,
		setu:     sc,
		wallet:   wc,
		producer: noopPublisher{},
		cfg:      cfg,
	}
}

// SetProducer swaps in a real event publisher. Called from main.go after the
// Kafka writer is initialised.
func (s *Service) SetProducer(p EventPublisher) { s.producer = p }

// Store returns the underlying store. Exposed so handlers can issue thin
// read-only fan-outs (categories, mobile plans, single payment detail)
// without forcing every query through the service layer.
func (s *Service) Store() *store.Store { return s.store }

// Setu exposes the underlying Setu client. Used by the webhook receiver to
// verify signatures.
func (s *Service) Setu() setu.SetuClient { return s.setu }

// Cfg returns a copy of the active config — handy in tests.
func (s *Service) Cfg() Config { return s.cfg }
