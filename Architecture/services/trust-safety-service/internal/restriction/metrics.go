package restriction

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics are the plan's section 9.5 "outbox health" and reconciliation
// counters for restriction commands, on the default Prometheus registry
// (served by shared/o11y/metrics.Handler):
//
//	atpost_trust_safety_service_restriction_commands_total{disposition}
//	atpost_trust_safety_service_restriction_command_escalations_total{reason}
//	atpost_trust_safety_service_restriction_commands_oldest_pending_seconds
//	atpost_trust_safety_service_restriction_commands_parked
//	atpost_trust_safety_service_copyright_reconcile_drift_total{kind}
//	atpost_trust_safety_service_copyright_reconcile_runs_total{outcome}
//
// Every method is nil-safe, so tests and callers without metrics pass nil.
type Metrics struct {
	commands       *prometheus.CounterVec
	escalations    *prometheus.CounterVec
	oldestPendingG prometheus.Gauge
	parkedG        prometheus.Gauge
	drift          *prometheus.CounterVec
	reconcileRuns  *prometheus.CounterVec
}

var (
	metricsOnce sync.Once
	metricsInst *Metrics
)

// NewMetrics registers the collectors once; later calls return the same set.
func NewMetrics() *Metrics {
	metricsOnce.Do(func() {
		const ns, sub = "atpost", "trust_safety_service"
		metricsInst = &Metrics{
			commands: promauto.NewCounterVec(prometheus.CounterOpts{
				Namespace: ns, Subsystem: sub, Name: "restriction_commands_total",
				Help: "Restriction command sends by disposition (acked, retry, superseded, parked).",
			}, []string{"disposition"}),
			escalations: promauto.NewCounterVec(prometheus.CounterOpts{
				Namespace: ns, Subsystem: sub, Name: "restriction_command_escalations_total",
				Help: "Restriction commands parked for a human, by reason.",
			}, []string{"reason"}),
			oldestPendingG: promauto.NewGauge(prometheus.GaugeOpts{
				Namespace: ns, Subsystem: sub, Name: "restriction_commands_oldest_pending_seconds",
				Help: "Age of the oldest pending restriction command (0 when drained).",
			}),
			parkedG: promauto.NewGauge(prometheus.GaugeOpts{
				Namespace: ns, Subsystem: sub, Name: "restriction_commands_parked",
				Help: "Restriction commands waiting for a human.",
			}),
			drift: promauto.NewCounterVec(prometheus.CounterOpts{
				Namespace: ns, Subsystem: sub, Name: "copyright_reconcile_drift_total",
				Help: "Hold reconciliation findings by kind (missing_hold, stale_revision, unexpected_active, missing_row, parked, pending_overdue).",
			}, []string{"kind"}),
			reconcileRuns: promauto.NewCounterVec(prometheus.CounterOpts{
				Namespace: ns, Subsystem: sub, Name: "copyright_reconcile_runs_total",
				Help: "Hold reconciliation sweeps by outcome (ok, drift, failed).",
			}, []string{"outcome"}),
		}
	})
	return metricsInst
}

func (m *Metrics) commandOutcome(d Disposition) {
	if m != nil {
		m.commands.WithLabelValues(d.String()).Inc()
	}
}

func (m *Metrics) escalation(reason string) {
	if m != nil {
		m.escalations.WithLabelValues(reason).Inc()
	}
}

func (m *Metrics) oldestPending(age time.Duration) {
	if m != nil {
		m.oldestPendingG.Set(age.Seconds())
	}
}

func (m *Metrics) parked(n int) {
	if m != nil {
		m.parkedG.Set(float64(n))
	}
}

// Drift counts one reconciliation finding.
func (m *Metrics) Drift(kind string) {
	if m != nil {
		m.drift.WithLabelValues(kind).Inc()
	}
}

// ReconcileRun counts one sweep.
func (m *Metrics) ReconcileRun(outcome string) {
	if m != nil {
		m.reconcileRuns.WithLabelValues(outcome).Inc()
	}
}
