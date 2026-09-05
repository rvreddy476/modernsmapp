package service

import (
	"context"
	"os"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// orderPaymentStore is the slice of *postgres.Store the money path
// (checkout → paid → confirmed, payment failed) depends on. Narrowed to an
// interface so the transition logic in ConfirmPayment /
// ApplyVerifiedPaymentEvent / MarkPaymentFailed is unit-testable with an
// in-memory fake; production wires the real store through NewWithDialer.
type orderPaymentStore interface {
	GetOrderByID(ctx context.Context, id uuid.UUID) (*postgres.Order, error)
	GetOrderItems(ctx context.Context, orderID uuid.UUID) ([]*postgres.OrderItem, error)
	// OrderPaymentIntentID is the intent commerce bound to the order at
	// checkout — the only intent a client callback may be checked against.
	OrderPaymentIntentID(ctx context.Context, orderID uuid.UUID) (uuid.UUID, error)
	MarkOrderPaid(ctx context.Context, orderID uuid.UUID, paymentID, gateway string, actorID *uuid.UUID, actorType string) (postgres.PaidTransition, error)
	TransitionPaymentStatus(ctx context.Context, orderID uuid.UUID, to, paymentID, gateway string) (bool, error)
	DeductStock(ctx context.Context, variantID uuid.UUID, qty int, orderID uuid.UUID) error
	ReleaseReservation(ctx context.Context, variantID, userID uuid.UUID, qty int) error
	EnqueueJobPool(ctx context.Context, kind string, payload []byte) error
}

// paymentsClient is the slice of *payments.Client commerce-service calls.
// The P0 client: commerce authors the intent, polls it, checks a client
// callback ADVISORILY, and files durable refund commands.
type paymentsClient interface {
	CreateIntent(ctx context.Context, in payments.CreateIntentInput) (*payments.Intent, error)
	GetIntent(ctx context.Context, id uuid.UUID) (*payments.Intent, error)
	VerifyCallback(ctx context.Context, intentID uuid.UUID, orderID, paymentID, signature string, expected money.Paise) (*payments.CallbackVerdict, error)
	Refund(ctx context.Context, intentID uuid.UUID, amount money.Paise, reason, idempotencyKey string) (*payments.RefundAccepted, error)
}

// WithAllowStubGateway lets ConfirmPayment accept gateway="stub". Wired
// from PAYMENTS_ALLOW_STUB in cmd/server/main.go (docker-compose sets it
// for the dev stack); production leaves it false so a client can never
// name the stub gateway. The env var is still honoured as a fallback so
// an operator setting it on the container alone keeps working.
func (s *Service) WithAllowStubGateway(allow bool) *Service {
	s.allowStubGateway = allow
	return s
}

func (s *Service) stubGatewayAllowed() bool {
	return s.allowStubGateway || os.Getenv("PAYMENTS_ALLOW_STUB") == "true"
}

// withOrderPaymentStore swaps the money-path store (tests only).
func (s *Service) withOrderPaymentStore(st orderPaymentStore) *Service {
	s.orders = st
	return s
}

// withPaymentsClient swaps the payments client (tests only).
func (s *Service) withPaymentsClient(c paymentsClient) *Service {
	s.payments = c
	return s
}
