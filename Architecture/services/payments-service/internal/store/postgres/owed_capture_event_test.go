package postgres

import (
	"errors"
	"testing"
)

func TestOwedCaptureState(t *testing.T) {
	for _, tc := range []struct {
		amount, refunded int64
		resolved         bool
		want             string
	}{
		{109629, 0, false, OwedStateRefundRequired},
		{109629, 50000, false, OwedStatePartiallyRefunded},
		{109629, 109629, false, OwedStateRefunded},
		{109629, 109629, true, OwedStateRefunded},
		{109629, 0, true, OwedStateRefunded},
		{0, 10, false, OwedStatePartiallyRefunded},
	} {
		if got := owedCaptureState(tc.amount, tc.refunded, tc.resolved); got != tc.want {
			t.Errorf("owedCaptureState(%d, %d, %v) = %s, want %s", tc.amount, tc.refunded, tc.resolved, got, tc.want)
		}
	}
}

func TestMismatchRecordable(t *testing.T) {
	ok := WebhookEffect{NewStatus: "succeeded", ProviderPaymentID: "pay_1", ProviderOrderID: "order_1",
		AmountMinor: 100, Currency: "INR"}
	if err := mismatchRecordable(ok); err != nil {
		t.Fatalf("complete capture: %v", err)
	}
	for name, mut := range map[string]func(*WebhookEffect){
		"not a capture":   func(e *WebhookEffect) { e.NewStatus = "failed" },
		"no payment id":   func(e *WebhookEffect) { e.ProviderPaymentID = " " },
		"no order id":     func(e *WebhookEffect) { e.ProviderOrderID = "" },
		"zero amount":     func(e *WebhookEffect) { e.AmountMinor = 0 },
		"negative amount": func(e *WebhookEffect) { e.AmountMinor = -1 },
		"no currency":     func(e *WebhookEffect) { e.Currency = "" },
	} {
		e := ok
		mut(&e)
		if err := mismatchRecordable(e); !errors.Is(err, ErrMismatchNotRecordable) {
			t.Errorf("%s: %v, want ErrMismatchNotRecordable", name, err)
		}
	}
}
