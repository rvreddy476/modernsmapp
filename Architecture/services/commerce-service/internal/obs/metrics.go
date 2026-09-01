// Package obs holds the Commerce money-integrity instrumentation.
//
// §13 of the plan. Two rules shape every metric here, and both are
// constraints rather than preferences:
//
//  1. NO PII IN ANY LABEL. Not a user id, phone, address, PAN, card handle,
//     PSP secret — and not a customer-visible order NUMBER either, because
//     that appears in support tickets and emails alongside the customer's
//     name. Order id is permitted: it is internal and correlates to a row
//     without identifying a person.
//  2. Cardinality is bounded. Every label is drawn from a fixed vocabulary
//     (a result, a reason, a state), never from user input.
//
// The four counters that page — amount mismatch, oversell attempt, webhook
// verification failure, reconciliation drift — should sit at zero forever.
// They exist so that the first time they do not, someone finds out in
// minutes rather than from a customer.
package obs

import (
	"log/slog"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics is the commerce instrument set.
type Metrics struct {
	CheckoutTotal    *prometheus.CounterVec
	CheckoutDuration prometheus.Histogram

	// AmountMismatch is a PAGING alarm. A payment event whose amount,
	// currency, payer or intent does not match the order it claims to
	// settle is either a defect in the payment path or an attempt to pay
	// ₹1 for a ₹10,000 order. Steady state is zero.
	AmountMismatchTotal prometheus.Counter

	// OversellAttempt counts oversells PREVENTED by the database
	// constraint. It is not "we oversold" — it is "the invariant did its
	// job", which is why a non-zero value is still worth waking someone:
	// it means the application logic and the constraint disagree.
	OversellAttempt prometheus.Counter

	DuplicateSuppressedTotal prometheus.Counter
	PaymentApplied           *prometheus.CounterVec

	ReservationAgeSeconds prometheus.Gauge
	RefundPendingSeconds  prometheus.Gauge
	RefundInitiateFailed  prometheus.Counter

	OrderTransitionRejected *prometheus.CounterVec
	OutboxUnpublished       prometheus.Gauge

	QuoteRejected *prometheus.CounterVec
}

// New registers the instrument set.
func New() *Metrics {
	return &Metrics{
		CheckoutTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "commerce_checkout_total",
			Help: "Checkout attempts by outcome.",
		}, []string{"result"}),

		CheckoutDuration: promauto.NewHistogram(prometheus.HistogramOpts{
			Name: "commerce_checkout_duration_seconds",
			Help: "End-to-end checkout latency, including the single database transaction.",
			// Buckets chosen around the p99 < 3s acceptance criterion, with
			// headroom above it so a breach is visible rather than clipped.
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5, 10},
		}),

		AmountMismatchTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "commerce_payment_amount_mismatch_total",
			Help: "PAGES. Payment events refused because the amount, currency, payer or intent did not match the order.",
		}),

		OversellAttempt: promauto.NewCounter(prometheus.CounterOpts{
			Name: "commerce_oversell_attempt_total",
			Help: "PAGES. Oversells prevented by the reserved_qty <= total_qty constraint.",
		}),

		DuplicateSuppressedTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "commerce_payment_duplicate_suppressed_total",
			Help: "Payment events suppressed by the durable inbox. Informational; a spike suggests provider retries.",
		}),

		PaymentApplied: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "commerce_payment_event_applied_total",
			Help: "Payment events successfully applied, by event type.",
		}, []string{"event_type"}),

		ReservationAgeSeconds: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "commerce_reservation_age_seconds",
			Help: "Age of the oldest live inventory reservation. Above twice the TTL means the expiry sweeper is stuck.",
		}),

		RefundPendingSeconds: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "commerce_refund_pending_age_seconds",
			Help: "Age of the oldest unsettled refund command. Money we owe and have not returned.",
		}),

		RefundInitiateFailed: promauto.NewCounter(prometheus.CounterOpts{
			Name: "commerce_refund_initiate_failed_total",
			Help: "Refund deliveries to payments that failed. Never silently dropped — the command stays claimable.",
		}),

		OrderTransitionRejected: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "commerce_order_transition_rejected_total",
			Help: "Order status transitions refused by the state machine.",
		}, []string{"from", "to"}),

		OutboxUnpublished: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "commerce_outbox_unpublished_rows",
			Help: "Outbox rows awaiting publication. A rising value means events are not reaching consumers.",
		}),

		QuoteRejected: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "commerce_quote_rejected_total",
			Help: "Checkouts refused because the delivery quote was stale, expired or already consumed.",
		}, []string{"reason"}),
	}
}

// ─── consumers.Observer ──────────────────────────────────────────────

// AmountMismatch records a refused payment event.
//
// The order id is logged, never used as a metric label: it is unbounded
// cardinality, and the counter's job is to fire an alarm, not to identify
// which order — the log line does that.
func (m *Metrics) AmountMismatch(orderID uuid.UUID, eventMinor, orderMinor int64) {
	m.bumpMismatch()
	slog.Error("commerce: payment amount mismatch",
		"order_id", orderID, "event_minor", eventMinor, "order_minor", orderMinor)
}

// bumpMismatch increments the counter. It is separate from the interface
// method because a Go type cannot have a field and a method of the same name.
func (m *Metrics) bumpMismatch() { m.AmountMismatchTotal.Inc() }

func (m *Metrics) DuplicateSuppressed(eventID string) { m.DuplicateSuppressedTotal.Inc() }

func (m *Metrics) Applied(eventType string) { m.PaymentApplied.WithLabelValues(eventType).Inc() }
