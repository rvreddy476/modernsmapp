// Package telemetry exposes the spec §17 KPIs as Prometheus gauges and
// histograms. The /metrics endpoint already runs in cmd/server/main.go via
// the shared o11y/metrics handler; this package only registers the
// dating-specific instruments.
//
// Spec §17 — north-star metrics:
//   - 30-day off-app-meet rate
//   - conversation quality score
//
// Spec §17 — operational gauges:
//   - pulse_daily_active_users
//   - pulse_sparks_per_day
//   - pulse_matches_per_day
//   - pulse_conversation_rate
//   - pulse_match_quality_p95
//   - pulse_premium_conversion_rate
//
// Spec §17 — endpoint latency:
//   - pulse_today_endpoint_latency_ms (histogram)
package telemetry

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds every Pulse-specific Prometheus instrument.
type Metrics struct {
	DAU                      prometheus.Gauge
	SparksPerDay             prometheus.Gauge
	MatchesPerDay            prometheus.Gauge
	ConversationRate         prometheus.Gauge
	MatchQualityP95          prometheus.Gauge
	PremiumConversionRate    prometheus.Gauge
	OffAppMeetRate30d        prometheus.Gauge
	ConversationQualityScore prometheus.Gauge
	PulseTodayLatency        prometheus.Histogram
}

var (
	metricsOnce sync.Once
	singleton   *Metrics
)

// Default returns the package-singleton Metrics. Safe to call from any
// goroutine; the gauges/histograms are package-level so a second call is
// a no-op.
func Default() *Metrics {
	metricsOnce.Do(func() {
		singleton = newMetrics()
	})
	return singleton
}

func newMetrics() *Metrics {
	return &Metrics{
		DAU: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_daily_active_users",
			Help: "Pulse daily active users (last 24h profile-active touch).",
		}),
		SparksPerDay: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_sparks_per_day",
			Help: "Sparks created in the rolling 24h window.",
		}),
		MatchesPerDay: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_matches_per_day",
			Help: "Matches formed in the rolling 24h window.",
		}),
		ConversationRate: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_conversation_rate",
			Help: "Fraction of matches that exchanged a first message within 7 days.",
		}),
		MatchQualityP95: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_match_quality_p95",
			Help: "p95 of the matcher score across formed matches in the 24h window.",
		}),
		PremiumConversionRate: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_premium_conversion_rate",
			Help: "Fraction of DAU on premium.",
		}),
		OffAppMeetRate30d: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_off_app_meet_rate_30d",
			Help: "30-day off-app meet rate from safe-meet check-in events (north-star).",
		}),
		ConversationQualityScore: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_conversation_quality_score",
			Help: "Conversation quality score (replies × days × rating placeholder).",
		}),
		PulseTodayLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "pulse_today_endpoint_latency_ms",
			Help:    "Latency of /v1/dating/pulse/today in milliseconds.",
			Buckets: []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
		}),
	}
}
