// north_star.go — computes the spec §17 north-star metrics nightly.
//
// Two metrics are emitted:
//
//   1. Off-app meet rate (30 days). The numerator is the count of
//      `dating_safety_events.kind='meet_check_in' AND status='safe'` rows
//      in the last 30 days; the denominator is total scheduled meets.
//
//   2. Conversation quality score (placeholder formula until Sprint 6 wires
//      replies × days × user-rating). For v1 we approximate as
//      conversations_with_5plus_messages / total_conversations, which the
//      message-service exposes as a counter (TODO: wire over HTTP). When
//      that count is unavailable we fall back to 0.
//
// The same payload is also written into Prometheus gauges so dashboards see
// the latest north-star value without parsing Kafka.
package telemetry

import (
	"context"
	"fmt"
	"time"

	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/store"
)

// NorthStarComputer assembles the nightly KPIs.
type NorthStarComputer struct {
	store    *store.Store
	producer *datingevents.Producer
	metrics  *Metrics
}

// NewNorthStarComputer wires the dependencies. producer/metrics may be nil
// for tests — the compute step still runs.
func NewNorthStarComputer(s *store.Store, p *datingevents.Producer, m *Metrics) *NorthStarComputer {
	return &NorthStarComputer{store: s, producer: p, metrics: m}
}

// Snapshot is the computed result.
type Snapshot struct {
	WindowDays               int       `json:"window_days"`
	OffAppMeetRate           float64   `json:"off_app_meet_rate"`
	ConversationQualityScore float64   `json:"conversation_quality_score"`
	GeneratedAt              time.Time `json:"generated_at"`
	SafeCheckInsCount        int64     `json:"safe_check_ins_count"`
	ScheduledMeetsCount      int64     `json:"scheduled_meets_count"`
}

// Compute runs the queries and updates the gauges. It does NOT publish
// the Kafka event; call Emit for that.
func (c *NorthStarComputer) Compute(ctx context.Context) (*Snapshot, error) {
	if c.store == nil {
		return nil, fmt.Errorf("invalid: store is nil")
	}
	const window = 30
	since := time.Now().Add(-time.Duration(window) * 24 * time.Hour)

	safe, err := c.store.CountSafetyMeetCheckInsSafeWindow(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("count safe check-ins: %w", err)
	}
	scheduled, err := c.store.CountSafetyEventsByKindWindow(ctx, "meet_scheduled", since)
	if err != nil {
		return nil, fmt.Errorf("count scheduled meets: %w", err)
	}

	rate := 0.0
	if scheduled > 0 {
		rate = float64(safe) / float64(scheduled)
	}

	// v1 conversation quality: a placeholder. Sprint 6 wires the real
	// signal from message-service. Until then we expose 0 so dashboards
	// know the metric is wired but unfilled.
	convQuality := 0.0

	snap := &Snapshot{
		WindowDays:               window,
		OffAppMeetRate:           rate,
		ConversationQualityScore: convQuality,
		GeneratedAt:              time.Now().UTC(),
		SafeCheckInsCount:        safe,
		ScheduledMeetsCount:      scheduled,
	}

	if c.metrics != nil {
		c.metrics.OffAppMeetRate30d.Set(rate)
		c.metrics.ConversationQualityScore.Set(convQuality)
	}
	return snap, nil
}

// Emit publishes the snapshot as a dating.telemetry.north_star event.
func (c *NorthStarComputer) Emit(ctx context.Context, snap *Snapshot) error {
	if c.producer == nil || snap == nil {
		return nil
	}
	return c.producer.PublishTelemetryNorthStar(ctx, datingevents.TelemetryNorthStarPayload{
		WindowDays:               snap.WindowDays,
		OffAppMeetRate:           snap.OffAppMeetRate,
		ConversationQualityScore: snap.ConversationQualityScore,
		GeneratedAt:              snap.GeneratedAt,
	})
}

// Run is the convenience wrapper used by cmd/north-star.
func (c *NorthStarComputer) Run(ctx context.Context) (*Snapshot, error) {
	snap, err := c.Compute(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.Emit(ctx, snap); err != nil {
		return snap, fmt.Errorf("emit north-star: %w", err)
	}
	return snap, nil
}
