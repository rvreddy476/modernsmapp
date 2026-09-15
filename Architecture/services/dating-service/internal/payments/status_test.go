package payments

import "testing"

func TestPremiumCustomerStatus(t *testing.T) {
	cases := []struct {
		snap           PurchasePaymentSnapshot
		status, refund string
	}{
		{PurchasePaymentSnapshot{Status: StatusCreated}, CustomerStatusConfirming, ""},
		{PurchasePaymentSnapshot{Status: StatusConfirming}, CustomerStatusConfirming, ""},
		// A paid row with no applied signed capture is still confirming.
		{PurchasePaymentSnapshot{Status: StatusPaid}, CustomerStatusConfirming, ""},
		{PurchasePaymentSnapshot{Status: StatusPaid, CaptureApplied: true}, CustomerStatusPaid, ""},
		{PurchasePaymentSnapshot{Status: StatusPartiallyRefunded, CaptureApplied: true}, CustomerStatusPaid, RefundStatusPartiallyRefunded},
		{PurchasePaymentSnapshot{Status: StatusRefunded, CaptureApplied: true}, CustomerStatusPaid, RefundStatusRefunded},
		{PurchasePaymentSnapshot{Status: StatusFailed}, CustomerStatusFailed, ""},
	}
	for _, c := range cases {
		s, r := CustomerStatus(c.snap)
		if s != c.status || r != c.refund {
			t.Fatalf("%+v -> (%s,%s), want (%s,%s)", c.snap, s, r, c.status, c.refund)
		}
	}
}

func TestPremiumPublicClientSession(t *testing.T) {
	in := &Intent{ProviderRef: "order_1", ClientSession: map[string]string{
		"provider": "razorpay", "order_id": "order_1", "key_id": "rzp_test_x", "merchant_display_name": "  Momentum Dating  ", "key_secret": "never",
	}}
	cs := in.PublicClientSession()
	if cs == nil || cs.Provider != "razorpay" || cs.OrderID != "order_1" || cs.KeyID != "rzp_test_x" || cs.MerchantDisplayName != "Momentum Dating" {
		t.Fatalf("session = %+v", cs)
	}
	if (&Intent{ProviderRef: "order_2", ClientSession: in.ClientSession}).PublicClientSession() != nil {
		t.Fatalf("a session for another provider order must be dropped")
	}
	if (&Intent{ProviderRef: "order_1"}).PublicClientSession() != nil {
		t.Fatalf("no session (stub gateway) must be nil")
	}
	if (&Intent{ProviderRef: "order_1", ClientSession: map[string]string{"provider": "razorpay", "order_id": "order_1"}}).PublicClientSession() != nil {
		t.Fatalf("a session without key_id must be nil")
	}
}
