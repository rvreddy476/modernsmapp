package events

import (
	"encoding/json"
	"testing"
)

// commerce-service writes commerce.order.duplicate_capture and
// payments-service refunds the payment it names; both use this type, and this
// pins the wire names payments reads.
func TestCommerceOrderDuplicateCapture_WireContract(t *testing.T) {
	if EventCommerceOrderDuplicateCapture != "commerce.order.duplicate_capture" {
		t.Fatalf("event type = %q", EventCommerceOrderDuplicateCapture)
	}
	in := CommerceOrderDuplicateCapturePayload{
		OrderID:        "11111111-1111-4111-8111-111111111111",
		IntentID:       "22222222-2222-4222-8222-222222222222",
		BoundIntentID:  "33333333-3333-4333-8333-333333333333",
		PayerID:        "44444444-4444-4444-8444-444444444444",
		AmountMinor:    118000,
		Currency:       "INR",
		RefundDueMinor: 118000,
		Reason:         "a second payment for an order that is already paid",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"order_id", "intent_id", "bound_intent_id", "payer_id", "amount_minor",
		"currency", "refund_due_minor", "reason"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("wire payload lacks %q: %s", key, b)
		}
	}
	if wire["amount_minor"] != float64(118000) || wire["refund_due_minor"] != float64(118000) {
		t.Errorf("amounts must be bare integers of paise: %s", b)
	}
	var out CommerceOrderDuplicateCapturePayload
	if err := json.Unmarshal(b, &out); err != nil || out != in {
		t.Fatalf("round trip = %+v, %v; want %+v", out, err, in)
	}
}
