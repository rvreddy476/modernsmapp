package gateway

import (
	"context"
	"fmt"
	"time"
)

// StubGateway is a mock payment gateway for development and testing.
// It always returns success and generates fake IDs.
type StubGateway struct{}

func (g *StubGateway) CreateOrder(_ context.Context, amount int64, currency, receipt string) (GatewayOrder, error) {
	return GatewayOrder{
		ID:       fmt.Sprintf("order_stub_%d", time.Now().UnixNano()),
		Amount:   amount,
		Currency: currency,
		Receipt:  receipt,
	}, nil
}

func (g *StubGateway) VerifySignature(_, _, _ string) bool { return true }

func (g *StubGateway) InitiateRefund(_ context.Context, paymentID string, amount int64) (GatewayRefund, error) {
	return GatewayRefund{
		ID:        fmt.Sprintf("rfnd_stub_%d", time.Now().UnixNano()),
		PaymentID: paymentID,
		Amount:    amount,
		Status:    "processed",
	}, nil
}

func (g *StubGateway) FetchPayment(_ context.Context, paymentID string) (GatewayPayment, error) {
	return GatewayPayment{ID: paymentID, Status: "captured"}, nil
}
