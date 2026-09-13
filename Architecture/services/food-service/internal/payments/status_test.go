package payments

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCustomerStatus_Table(t *testing.T) {
	cases := []struct {
		name         string
		snap         CustomerPaymentSnapshot
		status       string
		refundStatus string
	}{
		{"placed", CustomerPaymentSnapshot{"PLACED", "PENDING", "ONLINE", false}, CustomerStatusConfirming, ""},
		{"payment pending", CustomerPaymentSnapshot{"PAYMENT_PENDING", "PENDING", "ONLINE", false}, CustomerStatusConfirming, ""},
		{"authorized", CustomerPaymentSnapshot{"PAYMENT_PENDING", "AUTHORIZED", "ONLINE", false}, CustomerStatusConfirming, ""},
		// The guard: a captured payment_status without the applied event is not paid.
		{"captured without applied event", CustomerPaymentSnapshot{"CONFIRMED", "CAPTURED", "ONLINE", false}, CustomerStatusConfirming, ""},
		{"captured without applied event, pending order", CustomerPaymentSnapshot{"PAYMENT_PENDING", "CAPTURED", "ONLINE", false}, CustomerStatusConfirming, ""},
		// An applied event whose money was not taken (not reachable today) is not paid either.
		{"applied event but pending payment", CustomerPaymentSnapshot{"PAYMENT_PENDING", "PENDING", "ONLINE", true}, CustomerStatusConfirming, ""},
		{"paid", CustomerPaymentSnapshot{"CONFIRMED", "CAPTURED", "ONLINE", true}, CustomerStatusPaid, ""},
		{"paid delivered", CustomerPaymentSnapshot{"DELIVERED", "CAPTURED", "ONLINE", true}, CustomerStatusPaid, ""},
		{"paid lower-case method", CustomerPaymentSnapshot{"PREPARING", "CAPTURED", "online", true}, CustomerStatusPaid, ""},
		{"paid then cancelled, refund pending", CustomerPaymentSnapshot{"REFUND_PENDING", "REFUND_PENDING", "ONLINE", true}, CustomerStatusPaid, RefundStatusPending},
		{"paid partially refunded", CustomerPaymentSnapshot{"DELIVERED", "PARTIALLY_REFUNDED", "ONLINE", true}, CustomerStatusPaid, RefundStatusPartiallyRefunded},
		{"paid refunded", CustomerPaymentSnapshot{"REFUNDED", "REFUNDED", "ONLINE", true}, CustomerStatusPaid, RefundStatusRefunded},
		{"payment failed", CustomerPaymentSnapshot{"PAYMENT_FAILED", "FAILED", "ONLINE", false}, CustomerStatusFailed, ""},
		{"cancelled unpaid by admin", CustomerPaymentSnapshot{"CANCELLED_BY_ADMIN", "PENDING", "ONLINE", false}, CustomerStatusFailed, ""},
		{"cancelled unpaid by customer", CustomerPaymentSnapshot{"CANCELLED_BY_CUSTOMER", "PENDING", "ONLINE", false}, CustomerStatusFailed, ""},
		{"rejected unpaid", CustomerPaymentSnapshot{"RESTAURANT_REJECTED", "PENDING", "ONLINE", false}, CustomerStatusFailed, ""},
		// A captured-looking row on a cancelled order with no applied event: failed, not paid.
		{"cancelled, captured without event", CustomerPaymentSnapshot{"CANCELLED_BY_ADMIN", "CAPTURED", "ONLINE", false}, CustomerStatusFailed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, refund, err := CustomerStatus(tc.snap)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if status != tc.status || refund != tc.refundStatus {
				t.Fatalf("CustomerStatus(%+v) = (%q, %q), want (%q, %q)", tc.snap, status, refund, tc.status, tc.refundStatus)
			}
		})
	}
}

func TestCustomerStatus_NeverPaidWithoutAppliedCapture(t *testing.T) {
	statuses := []string{"DRAFT", "PLACED", "PAYMENT_PENDING", "PAYMENT_FAILED", "CONFIRMED", "PREPARING", "DELIVERED",
		"CANCELLED_BY_ADMIN", "REFUND_PENDING", "REFUNDED", "FAILED"}
	payStatuses := []string{"NOT_REQUIRED", "PENDING", "AUTHORIZED", "CAPTURED", "FAILED", "REFUND_PENDING", "PARTIALLY_REFUNDED", "REFUNDED"}
	for _, os := range statuses {
		for _, ps := range payStatuses {
			status, _, err := CustomerStatus(CustomerPaymentSnapshot{OrderStatus: os, PaymentStatus: ps, PaymentMethod: "ONLINE"})
			if err != nil || status == CustomerStatusPaid {
				t.Fatalf("%s/%s without an applied capture = %q (err %v)", os, ps, status, err)
			}
		}
	}
}

func TestCustomerStatus_NonOnlineMethodsRefused(t *testing.T) {
	for _, m := range []string{"COD", "WALLET", ""} {
		if _, _, err := CustomerStatus(CustomerPaymentSnapshot{OrderStatus: "CONFIRMED", PaymentStatus: "CAPTURED", PaymentMethod: m, CaptureApplied: true}); !errors.Is(err, ErrPaymentNotOnline) {
			t.Fatalf("%q: err = %v, want ErrPaymentNotOnline", m, err)
		}
	}
}

func TestPublicClientSession(t *testing.T) {
	full := map[string]string{"provider": "razorpay", "order_id": "order_RZP1", "key_id": "rzp_test_pub"}
	intent := func(session map[string]string) *Intent {
		return &Intent{ID: uuid.New(), ProviderRef: "order_RZP1", ClientSession: session}
	}

	withSecret := map[string]string{"key_secret": "sk_live_SECRET", "amount": "25000"}
	for k, v := range full {
		withSecret[k] = v
	}
	got := intent(withSecret).PublicClientSession()
	if got == nil || *got != (ClientSession{Provider: "razorpay", OrderID: "order_RZP1", KeyID: "rzp_test_pub"}) {
		t.Fatalf("session = %+v", got)
	}
	raw, _ := json.Marshal(got)
	var keys map[string]any
	_ = json.Unmarshal(raw, &keys)
	if len(keys) != 3 || strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "25000") {
		t.Fatalf("session JSON = %s", raw)
	}

	for name, session := range map[string]map[string]string{
		"none":             nil,
		"empty":            {},
		"no key id":        {"provider": "razorpay", "order_id": "order_RZP1"},
		"no provider":      {"order_id": "order_RZP1", "key_id": "rzp_test_pub"},
		"no order id":      {"provider": "razorpay", "key_id": "rzp_test_pub"},
		"blank key id":     {"provider": "razorpay", "order_id": "order_RZP1", "key_id": "  "},
		"another order id": {"provider": "razorpay", "order_id": "order_OTHER", "key_id": "rzp_test_pub"},
	} {
		if s := intent(session).PublicClientSession(); s != nil {
			t.Fatalf("%s: session = %+v, want nil", name, s)
		}
	}
	var nilIntent *Intent
	if nilIntent.PublicClientSession() != nil {
		t.Fatal("nil intent produced a session")
	}
}

func TestPublicIntentOmitsPartiesAndSession(t *testing.T) {
	i := &Intent{ID: uuid.New(), PayerID: uuid.New(), PayeeID: uuid.New(), ProviderRef: "order_RZP1",
		ClientSession: map[string]string{"key_secret": "sk_live_SECRET"}}
	raw, _ := json.Marshal(i.PublicIntent())
	for _, forbidden := range []string{"payer_id", "payee_id", "client_session", "SECRET", i.PayeeID.String(), i.PayerID.String()} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("public intent carries %q: %s", forbidden, raw)
		}
	}
}
