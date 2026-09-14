// Package obs holds payments-service's money-integrity instruments.
//
// Labels are drawn from a fixed vocabulary only — never an intent id, a
// reference id, a user id or anything a caller supplied.
package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	dto "github.com/prometheus/client_model/go"
)

// RefundReasonCodes is the closed vocabulary of reasons a refund command is
// parked in needs_attention. It is also the `reason_code` of the
// payment.refund_failed event.
var RefundReasonCodes = []string{
	"provider_rejected",
	"payment_not_found",
	"ambiguous_payment",
	"invalid_payment_id",
	"stub_on_real_provider",
	"intent_not_found",
	"no_provider_reference",
	"no_provider_adapter",
	"already_refunded_unmatched",
}

var (
	refundsNeedsAttention = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "payments_refunds_needs_attention",
		Help: "PAGES. Refund commands parked in needs_attention: money still owed that the worker will never retry. " +
			"Refreshed by the refund worker every tick. Runbook: docs/runbooks/payments-refund-needs-attention.md",
	})

	refundParkedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "payments_refund_parked_total",
		Help: "Refund commands parked in needs_attention by this process, by reason code.",
	}, []string{"reason_code"})
)

// SetRefundsNeedingAttention records the current number of parked commands.
func SetRefundsNeedingAttention(n int64) { refundsNeedsAttention.Set(float64(n)) }

// RefundsNeedingAttention reads the gauge back.
func RefundsNeedingAttention() float64 {
	var m dto.Metric
	if err := refundsNeedsAttention.Write(&m); err != nil {
		return -1
	}
	return m.GetGauge().GetValue()
}

// RefundParked counts one newly parked command. An unknown code is counted as
// "other" so the label set stays bounded.
func RefundParked(code string) {
	refundParkedTotal.WithLabelValues(boundedCode(code)).Inc()
}

// RefundParkedCount reads one reason code's counter back.
func RefundParkedCount(code string) float64 {
	var m dto.Metric
	if err := refundParkedTotal.WithLabelValues(boundedCode(code)).Write(&m); err != nil {
		return -1
	}
	return m.GetCounter().GetValue()
}

func boundedCode(code string) string {
	for _, c := range RefundReasonCodes {
		if c == code {
			return code
		}
	}
	return "other"
}
