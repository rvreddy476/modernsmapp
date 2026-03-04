package gateway

import (
	"context"
	"testing"
)

// TestStubGatewayImplementsInterface is a compile-time assertion that *StubGateway
// satisfies the PaymentGateway interface.
var _ PaymentGateway = (*StubGateway)(nil)

func TestStubGatewayImplementsInterface(t *testing.T) {
	// The var-level compile-time check above is sufficient.
	// This test body exists so the check is reported in go test output.
	var gw PaymentGateway = &StubGateway{}
	if gw == nil {
		t.Fatal("StubGateway does not satisfy PaymentGateway interface")
	}
}

// TestStubCreateOrder verifies that CreateOrder returns a non-empty ID and
// that the Amount and Currency echo back the inputs.
func TestStubCreateOrder(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
		receipt  string
	}{
		{"basic order", 10000, "INR", "receipt_001"},
		{"zero amount", 0, "USD", "receipt_000"},
		{"large amount", 999999999, "INR", "receipt_big"},
	}

	gw := &StubGateway{}
	ctx := context.Background()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			order, err := gw.CreateOrder(ctx, tc.amount, tc.currency, tc.receipt)
			if err != nil {
				t.Fatalf("CreateOrder returned unexpected error: %v", err)
			}
			if order.ID == "" {
				t.Error("expected non-empty GatewayOrder.ID")
			}
			if order.Amount != tc.amount {
				t.Errorf("expected Amount=%d, got %d", tc.amount, order.Amount)
			}
			if order.Currency != tc.currency {
				t.Errorf("expected Currency=%q, got %q", tc.currency, order.Currency)
			}
			if order.Receipt != tc.receipt {
				t.Errorf("expected Receipt=%q, got %q", tc.receipt, order.Receipt)
			}
		})
	}
}

// TestStubVerifySignature verifies that VerifySignature always returns true
// regardless of the supplied arguments.
func TestStubVerifySignature(t *testing.T) {
	tests := []struct {
		name      string
		orderID   string
		paymentID string
		signature string
	}{
		{"valid-looking inputs", "order_123", "pay_abc", "sig_xyz"},
		{"empty strings", "", "", ""},
		{"arbitrary garbage", "!!!!", "????", "####"},
	}

	gw := &StubGateway{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok := gw.VerifySignature(tc.orderID, tc.paymentID, tc.signature)
			if !ok {
				t.Errorf("VerifySignature(%q, %q, %q) returned false, want true",
					tc.orderID, tc.paymentID, tc.signature)
			}
		})
	}
}

// TestStubInitiateRefund verifies that InitiateRefund returns a non-empty ID and
// that Amount and PaymentID echo the inputs.
func TestStubInitiateRefund(t *testing.T) {
	tests := []struct {
		name      string
		paymentID string
		amount    int64
	}{
		{"standard refund", "pay_001", 5000},
		{"zero refund", "pay_002", 0},
		{"full refund", "pay_003", 1000000},
	}

	gw := &StubGateway{}
	ctx := context.Background()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refund, err := gw.InitiateRefund(ctx, tc.paymentID, tc.amount)
			if err != nil {
				t.Fatalf("InitiateRefund returned unexpected error: %v", err)
			}
			if refund.ID == "" {
				t.Error("expected non-empty GatewayRefund.ID")
			}
			if refund.Amount != tc.amount {
				t.Errorf("expected Amount=%d, got %d", tc.amount, refund.Amount)
			}
			if refund.PaymentID != tc.paymentID {
				t.Errorf("expected PaymentID=%q, got %q", tc.paymentID, refund.PaymentID)
			}
		})
	}
}

// TestStubFetchPayment verifies that FetchPayment returns a GatewayPayment
// whose Status is "captured" and whose ID matches the requested payment ID.
func TestStubFetchPayment(t *testing.T) {
	tests := []struct {
		name      string
		paymentID string
	}{
		{"normal id", "pay_12345"},
		{"empty id", ""},
	}

	gw := &StubGateway{}
	ctx := context.Background()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payment, err := gw.FetchPayment(ctx, tc.paymentID)
			if err != nil {
				t.Fatalf("FetchPayment returned unexpected error: %v", err)
			}
			if payment.Status != "captured" {
				t.Errorf("expected Status=%q, got %q", "captured", payment.Status)
			}
			if payment.ID != tc.paymentID {
				t.Errorf("expected ID=%q, got %q", tc.paymentID, payment.ID)
			}
		})
	}
}
