package payments

import (
	"testing"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

var (
	tOrderID  = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	tUserID   = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	tIntentID = "cccccccc-0000-0000-0000-000000000003"
)

func snapshot(status, paymentStatus string) OrderSnapshot {
	return OrderSnapshot{
		OrderID:       tOrderID,
		UserID:        tUserID,
		Status:        status,
		PaymentStatus: paymentStatus,
		AmountMinor:   25000,
		Currency:      "INR",
		IntentID:      tIntentID,
	}
}

func succeededEvent() Event {
	return Event{
		EventID:     "evt-1",
		EventType:   events.EventPaymentSucceeded,
		IntentID:    tIntentID,
		OrderID:     tOrderID,
		PayerID:     tUserID,
		AmountMinor: 25000,
		Currency:    "INR",
		Status:      "succeeded",
	}
}

func failedEvent() Event {
	ev := succeededEvent()
	ev.EventType = events.EventPaymentFailed
	ev.Status = "failed"
	return ev
}

func refundedEvent(status string, amountMinor int64) Event {
	return Event{
		EventID:     "evt-r",
		EventType:   events.EventPaymentRefunded,
		IntentID:    tIntentID,
		OrderID:     tOrderID,
		AmountMinor: amountMinor,
		Status:      status,
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		order   OrderSnapshot
		event   func() Event
		outcome Outcome
		effect  Effect
	}{
		// ── payment.succeeded ────────────────────────────────────────────
		{"succeeded confirms a pending order", snapshot(orderstate.PaymentPending, "PENDING"), succeededEvent,
			OutcomeConfirmed, EffectConfirm},
		{"succeeded confirms a placed order", snapshot(orderstate.Placed, "PENDING"), succeededEvent,
			OutcomeConfirmed, EffectConfirm},
		{"succeeded after failed confirms (retry on the same intent)", snapshot(orderstate.PaymentFailed, "FAILED"), succeededEvent,
			OutcomeConfirmed, EffectConfirm},
		{"order without a recorded intent still confirms", func() OrderSnapshot {
			o := snapshot(orderstate.PaymentPending, "PENDING")
			o.IntentID = ""
			return o
		}(), succeededEvent, OutcomeConfirmed, EffectConfirm},
		{"currency compared case-insensitively", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.Currency = "inr"
			return ev
		}, OutcomeConfirmed, EffectConfirm},

		{"amount short by one paisa is a mismatch", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.AmountMinor = 24999
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"zero amount is a mismatch", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.AmountMinor = 0
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"over-payment is a mismatch", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.AmountMinor = 25001
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"foreign currency is a mismatch", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.Currency = "USD"
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"missing currency is a mismatch", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.Currency = ""
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"another payer is a mismatch", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.PayerID = uuid.New()
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"missing payer is a mismatch", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.PayerID = uuid.Nil
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"another intent is a mismatch", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.IntentID = uuid.NewString()
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"mismatch wins even on an already-paid order", snapshot(orderstate.Confirmed, "CAPTURED"), func() Event {
			ev := succeededEvent()
			ev.AmountMinor = 1
			return ev
		}, OutcomeAmountMismatch, EffectNone},

		{"already captured is a no-op", snapshot(orderstate.PaymentPending, "CAPTURED"), succeededEvent,
			OutcomeAlreadyPaid, EffectNone},
		{"confirmed order is a no-op", snapshot(orderstate.Confirmed, "CAPTURED"), succeededEvent,
			OutcomeAlreadyPaid, EffectNone},
		{"order in the kitchen is a no-op", snapshot(orderstate.Preparing, "CAPTURED"), succeededEvent,
			OutcomeAlreadyPaid, EffectNone},

		{"late capture on customer cancel never revives", snapshot(orderstate.CancelledByCustomer, "PENDING"), succeededEvent,
			OutcomeLateCapture, EffectNone},
		{"late capture on restaurant cancel never revives", snapshot(orderstate.CancelledByRestaurant, "PENDING"), succeededEvent,
			OutcomeLateCapture, EffectNone},
		{"late capture on admin cancel never revives", snapshot(orderstate.CancelledByAdmin, "PENDING"), succeededEvent,
			OutcomeLateCapture, EffectNone},
		{"late capture on rejected never revives", snapshot(orderstate.RestaurantRejected, "PENDING"), succeededEvent,
			OutcomeLateCapture, EffectNone},
		{"late capture on failed never revives", snapshot(orderstate.Failed, "PENDING"), succeededEvent,
			OutcomeLateCapture, EffectNone},
		{"late capture on draft never revives", snapshot(orderstate.Draft, "PENDING"), succeededEvent,
			OutcomeLateCapture, EffectNone},

		// ── payment.failed ───────────────────────────────────────────────
		{"failed marks a pending order", snapshot(orderstate.PaymentPending, "PENDING"), failedEvent,
			OutcomeMarkedFailed, EffectMarkFailed},
		{"failed marks a placed order", snapshot(orderstate.Placed, "PENDING"), failedEvent,
			OutcomeMarkedFailed, EffectMarkFailed},
		{"failed after capture is ignored", snapshot(orderstate.Confirmed, "CAPTURED"), failedEvent,
			OutcomeIgnoredAfterCapture, EffectNone},
		{"failed after capture is ignored even while status lags", snapshot(orderstate.PaymentPending, "CAPTURED"), failedEvent,
			OutcomeIgnoredAfterCapture, EffectNone},
		{"failed on a cancelled order is ignored", snapshot(orderstate.CancelledByCustomer, "PENDING"), failedEvent,
			OutcomeFailedIgnored, EffectNone},
		{"failed twice is ignored", snapshot(orderstate.PaymentFailed, "FAILED"), failedEvent,
			OutcomeFailedIgnored, EffectNone},

		// ── payment.refunded ─────────────────────────────────────────────
		{"full refund finalises", snapshot(orderstate.RefundPending, "REFUND_PENDING"), func() Event {
			return refundedEvent("refunded", 25000)
		}, OutcomeRefunded, EffectRefund},
		{"full refund on a captured order still records", snapshot(orderstate.CancelledByAdmin, "CAPTURED"), func() Event {
			return refundedEvent("refunded", 25000)
		}, OutcomeRefunded, EffectRefund},
		{"partial refund", snapshot(orderstate.Delivered, "CAPTURED"), func() Event {
			return refundedEvent("partially_refunded", 5000)
		}, OutcomePartiallyRefunded, EffectPartialRefund},
		{"refund on an already refunded payment is ignored", snapshot(orderstate.Refunded, "REFUNDED"), func() Event {
			return refundedEvent("refunded", 25000)
		}, OutcomeRefundIgnored, EffectNone},
		{"refund on a never-captured payment is ignored", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			return refundedEvent("refunded", 25000)
		}, OutcomeRefundIgnored, EffectNone},
		{"refund larger than the order is a mismatch", snapshot(orderstate.RefundPending, "REFUND_PENDING"), func() Event {
			return refundedEvent("refunded", 25001)
		}, OutcomeAmountMismatch, EffectNone},
		{"refund for another intent is a mismatch", snapshot(orderstate.RefundPending, "REFUND_PENDING"), func() Event {
			ev := refundedEvent("refunded", 25000)
			ev.IntentID = uuid.NewString()
			return ev
		}, OutcomeAmountMismatch, EffectNone},
		{"refund with an unknown status is ignored", snapshot(orderstate.RefundPending, "REFUND_PENDING"), func() Event {
			return refundedEvent("submitted", 25000)
		}, OutcomeRefundIgnored, EffectNone},

		// ── anything else ────────────────────────────────────────────────
		{"unknown event type is ignored", snapshot(orderstate.PaymentPending, "PENDING"), func() Event {
			ev := succeededEvent()
			ev.EventType = "payment.status_changed"
			return ev
		}, OutcomeIgnored, EffectNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.order, tc.event())
			if d.Outcome != tc.outcome || d.Effect != tc.effect {
				t.Fatalf("Decide = (%s, %v), want (%s, %v); detail=%q", d.Outcome, d.Effect, tc.outcome, tc.effect, d.Detail)
			}
		})
	}
}
