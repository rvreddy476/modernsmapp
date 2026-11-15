package postgres

import "testing"

// The webhook-confirmed order's payment_pending → confirmed history line is
// written once: by the capture that applies, never by a redelivery of it.
// Replays the decision the capture transaction makes for each delivery,
// advancing the order the way the applied capture does.
func TestPaidConfirmationHistory_OnceUnderRedelivery(t *testing.T) {
	const intent = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name      string
		start     captureOrder
		wantLines int
	}{
		{"payment_pending order", captureOrder{Status: "payment_pending", PaymentStatus: "pending", BoundIntent: intent}, 1},
		{"created order", captureOrder{Status: "created", PaymentStatus: "pending", BoundIntent: intent}, 1},
		{"retry after a failed attempt", captureOrder{Status: "payment_pending", PaymentStatus: "failed", BoundIntent: intent}, 1},
		{"COD order confirmed at checkout: status does not move", captureOrder{Status: "confirmed", PaymentStatus: "pending", BoundIntent: intent}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.start
			lines := 0
			// The first delivery, then three redeliveries of the same payment
			// under fresh event ids (the inbox catches same-id repeats before
			// this decision is reached).
			for i := 0; i < 4; i++ {
				v := decideCapture(o, PaymentEvent{IntentID: intent})
				if confirmHistoryOnCapture(v, o.Status) {
					lines++
				}
				if v == captureApplies {
					o.Status, o.PaymentStatus = "confirmed", "paid"
				}
			}
			if lines != tc.wantLines {
				t.Fatalf("history lines = %d, want %d", lines, tc.wantLines)
			}
		})
	}
}

func TestConfirmHistoryOnCapture_OnlyTheApplyingCapture(t *testing.T) {
	for _, v := range []captureVerdict{captureRedelivered, captureDuplicate, captureLate} {
		if confirmHistoryOnCapture(v, "payment_pending") {
			t.Fatalf("verdict %d wrote a confirmation line", v)
		}
	}
	if !confirmHistoryOnCapture(captureApplies, "payment_pending") {
		t.Fatal("the applying capture wrote no confirmation line")
	}
}
