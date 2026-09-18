package events

import (
	"encoding/json"
	"testing"
	"time"
)

// Mopedu payments lane (2026-09-18): the wire name and shape rider-service
// publishes and notification-service consumes.
func TestRiderRidePaymentPaid_WireContract(t *testing.T) {
	if EventRiderRidePaymentPaid != "rider.ride.payment_paid" {
		t.Fatalf("event type = %q", EventRiderRidePaymentPaid)
	}
	in := RiderRidePaymentPaidPayload{
		RideID:         "11111111-1111-4111-8111-111111111111",
		CustomerUserID: "22222222-2222-4222-8222-222222222222",
		PartnerID:      "33333333-3333-4333-8333-333333333333",
		PartnerUserID:  "44444444-4444-4444-8444-444444444444",
		AmountPaise:    12350,
		Method:         "upi",
		PaidAt:         time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ride_id", "customer_user_id", "partner_id", "partner_user_id", "amount_paise", "method", "paid_at"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("wire payload lacks %q: %s", key, b)
		}
	}
	if wire["amount_paise"] != float64(12350) {
		t.Errorf("amount_paise = %v, want a bare integer of paise", wire["amount_paise"])
	}
	var out RiderRidePaymentPaidPayload
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}

	// partner_user_id is optional on the wire (a producer that has not been
	// enriched yet), never a decode failure.
	var partial RiderRidePaymentPaidPayload
	if err := json.Unmarshal([]byte(`{"ride_id":"r","customer_user_id":"c","partner_id":"p","amount_paise":100,"method":"cash"}`), &partial); err != nil {
		t.Fatal(err)
	}
	if partial.PartnerUserID != "" || partial.AmountPaise != 100 {
		t.Fatalf("partial decode = %+v", partial)
	}
}
