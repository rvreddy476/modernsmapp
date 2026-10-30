package postgres

import "testing"

// M1: a second, distinct capture on a paid intent is money owed back; the same
// payment announced again is not. The DB half (the refund_required row and the
// unchanged intent) is proven in service/duplicate_capture_m1_integration_test.go.
func TestIsDuplicateCapture(t *testing.T) {
	captured := func(payID string) WebhookEffect {
		return WebhookEffect{EventType: "payment.captured", NewStatus: "succeeded", ProviderPaymentID: payID}
	}
	for _, tc := range []struct {
		name     string
		status   string
		settled  string
		e        WebhookEffect
		duplicate bool
	}{
		{"second payment on a succeeded intent", "succeeded", "pay_A", captured("pay_B"), true},
		{"second payment on a partially refunded intent", "partially_refunded", "pay_A", captured("pay_B"), true},
		{"second payment on a refunded intent", "refunded", "pay_A", captured("pay_B"), true},
		{"second payment on a disputed intent", "disputed", "pay_A", captured("pay_B"), true},
		{"redelivery of the settling payment", "succeeded", "pay_A", captured("pay_A"), false},
		{"order.paid repeats the settling payment", "succeeded", "pay_A",
			WebhookEffect{EventType: "order.paid", NewStatus: "succeeded", ProviderPaymentID: "pay_A"}, false},
		{"settling payment with surrounding blanks", "succeeded", " pay_A ", captured("pay_A"), false},
		{"capture with no payment id", "succeeded", "pay_A", captured(""), false},
		{"paid intent with no payment id on record", "succeeded", "", captured("pay_B"), false},
		{"capture on a failed intent is the late-capture rule, not this one", "failed", "pay_A", captured("pay_B"), false},
		{"capture on a pending intent settles it", "pending", "", captured("pay_B"), false},
		{"capture on a processing intent settles it", "processing", "pay_A", captured("pay_B"), false},
		{"a non-capture event", "succeeded", "pay_A",
			WebhookEffect{EventType: "payment.failed", NewStatus: "", ProviderPaymentID: "pay_B"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDuplicateCapture(tc.status, tc.settled, tc.e); got != tc.duplicate {
				t.Fatalf("isDuplicateCapture(%q, %q, %+v) = %v, want %v", tc.status, tc.settled, tc.e, got, tc.duplicate)
			}
		})
	}
}

func TestRefundRequiredAuditEvent(t *testing.T) {
	if got := auditEventForRefundRequired(reasonDuplicateCapture); got != "duplicate_capture_refund_required" {
		t.Fatalf("duplicate capture audit event = %q", got)
	}
	if got := auditEventForRefundRequired(reasonLateCaptureAfterFailed); got != "late_capture_refund_required" {
		t.Fatalf("late capture audit event = %q (it must keep its existing name)", got)
	}
}
