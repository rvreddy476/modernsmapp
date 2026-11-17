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

// ─── Owed captures (payments.refund_required, com-2) ─────────────────

// OwedRefundFailureReasons is the closed vocabulary of
// payments_owed_refund_failures_total{reason}: "retryable" (the worker tries
// again) and every code a row is parked with.
var OwedRefundFailureReasons = append([]string{
	"retryable",
	"no_amount",
	"no_payment_id",
	"no_currency",
	"refund_list_failed",
}, RefundReasonCodes...)

// OwedRefundOutcomes is the closed vocabulary of
// payments_owed_refunds_issued_total{outcome}.
var OwedRefundOutcomes = []string{
	"submitted",        // the provider accepted the refund; its webhook resolves the row
	"stub_settled",     // a stub deployment settled it locally
	"already_refunded", // the provider had already refunded it; settled from its list
}

var (
	owedCapturesUnrefunded = promauto.NewGauge(prometheusGaugeOpts("payments_owed_captures_unrefunded",
		"PAGES via age. Captures owed back to buyers (payments.refund_required) not yet refunded, parked ones included. "+
			"Refreshed from the database by every replica."))
	owedCapturesParked = promauto.NewGauge(prometheusGaugeOpts("payments_owed_captures_parked",
		"PAGES. Owed captures the refund-required worker parked: it will not retry them without a person."))
	owedCapturesOldestAge = promauto.NewGauge(prometheusGaugeOpts("payments_owed_captures_oldest_age_seconds",
		"PAGES above 1 h. Age of the oldest owed capture not yet refunded (0 when none)."))

	owedRefundsIssued = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "payments_owed_refunds_issued_total",
		Help: "Refunds of owed captures this process placed or settled, by outcome.",
	}, []string{"outcome"})
	owedRefundFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "payments_owed_refund_failures_total",
		Help: "Failed refund attempts of owed captures, by reason (retryable, or the code the row was parked with).",
	}, []string{"reason"})
)

func prometheusGaugeOpts(name, help string) prometheus.GaugeOpts {
	return prometheus.GaugeOpts{Name: name, Help: help}
}

// SetOwedCaptures records the owed-capture backlog read from the database.
func SetOwedCaptures(unrefunded, parked int64, oldestAgeSeconds float64) {
	owedCapturesUnrefunded.Set(float64(unrefunded))
	owedCapturesParked.Set(float64(parked))
	if oldestAgeSeconds < 0 {
		oldestAgeSeconds = 0
	}
	owedCapturesOldestAge.Set(oldestAgeSeconds)
}

// OwedCaptures reads the three gauges back (tests).
func OwedCaptures() (unrefunded, parked, oldestAgeSeconds float64) {
	return gaugeValue(owedCapturesUnrefunded), gaugeValue(owedCapturesParked), gaugeValue(owedCapturesOldestAge)
}

// OwedRefundIssued counts one refund of an owed capture. An unknown outcome
// is counted as "other".
func OwedRefundIssued(outcome string) {
	owedRefundsIssued.WithLabelValues(bounded(outcome, OwedRefundOutcomes)).Inc()
}

// OwedRefundIssuedCount reads one outcome's counter back.
func OwedRefundIssuedCount(outcome string) float64 {
	return counterValue(owedRefundsIssued.WithLabelValues(bounded(outcome, OwedRefundOutcomes)))
}

// OwedRefundFailed counts one failed refund attempt of an owed capture. An
// unknown reason is counted as "other".
func OwedRefundFailed(reason string) {
	owedRefundFailures.WithLabelValues(bounded(reason, OwedRefundFailureReasons)).Inc()
}

// OwedRefundFailedCount reads one reason's counter back.
func OwedRefundFailedCount(reason string) float64 {
	return counterValue(owedRefundFailures.WithLabelValues(bounded(reason, OwedRefundFailureReasons)))
}

func gaugeValue(g prometheus.Gauge) float64 {
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		return -1
	}
	return m.GetGauge().GetValue()
}

func counterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		return -1
	}
	return m.GetCounter().GetValue()
}

func bounded(v string, vocabulary []string) string {
	for _, c := range vocabulary {
		if c == v {
			return v
		}
	}
	return "other"
}

func boundedCode(code string) string {
	for _, c := range RefundReasonCodes {
		if c == code {
			return code
		}
	}
	return "other"
}
