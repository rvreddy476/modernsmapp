package gateway

import "context"

// GatewayOrder is the order object returned by the PSP.
type GatewayOrder struct {
	ID       string
	Amount   int64
	Currency string
	Receipt  string
}

// GatewayRefund is the refund object returned by the PSP.
type GatewayRefund struct {
	ID        string
	PaymentID string
	Amount    int64
	Status    string
}

// GatewayPayment represents a payment fetched from the PSP.
type GatewayPayment struct {
	ID       string
	OrderID  string
	Amount   int64
	Currency string
	Status   string // "captured", "failed", "refunded"
}

// PaymentGateway defines the interface for external payment processors.
type PaymentGateway interface {
	// CreateOrder creates a PSP order for the given amount and receipt reference.
	CreateOrder(ctx context.Context, amount int64, currency, receipt string) (GatewayOrder, error)
	// VerifySignature validates a PSP webhook signature.
	VerifySignature(orderID, paymentID, signature string) bool
	// InitiateRefund triggers a refund for the given payment ID.
	InitiateRefund(ctx context.Context, paymentID string, amount int64) (GatewayRefund, error)
	// FetchPayment retrieves current payment status from PSP.
	FetchPayment(ctx context.Context, paymentID string) (GatewayPayment, error)
}
