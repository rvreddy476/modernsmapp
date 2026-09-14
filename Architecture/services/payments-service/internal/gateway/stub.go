package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// StubOrderPrefix begins every order id the stub gateway mints. Such an id
// was never a provider object, so nothing may send it to a real PSP.
const StubOrderPrefix = "order_stub_"

// IsStubOrderRef reports whether a provider order reference was minted by the
// stub gateway rather than by a real provider.
func IsStubOrderRef(ref string) bool { return strings.HasPrefix(ref, StubOrderPrefix) }

// StubGateway is a mock payment gateway for development and testing.
// It always returns success and generates fake IDs.
type StubGateway struct{}

func (g *StubGateway) CreateOrder(_ context.Context, amount int64, currency, receipt string) (GatewayOrder, error) {
	return GatewayOrder{
		ID:       fmt.Sprintf("%s%d", StubOrderPrefix, time.Now().UnixNano()),
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
