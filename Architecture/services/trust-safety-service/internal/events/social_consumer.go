// Package events holds trust-safety-service's Kafka consumers and the small
// HTTP clients they need to act on scoring decisions.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// FilterTrustScoreThreshold is the trust-score floor for the connection-request
// auto-filter. A sender whose trust.user_trust_state.trust_score is strictly
// below this value (score range 0–100, default 50) has their connection
// requests auto-filtered into the recipient's hidden queue. Senders with no
// trust-state row at all are NOT filtered on this rule — see fail-open notes in
// ConnectionFilterStore.GetSenderTrustSignal.
const FilterTrustScoreThreshold = 20

// Filter reason strings emitted on ConnectionRequestFiltered.Reason and sent to
// graph-service. Stable identifiers — downstream consumers may switch on them.
const (
	reasonSenderShadowbanned = "sender_shadowbanned"
	reasonLowTrustScore      = "low_trust_score"
	reasonPriorReport        = "prior_report"
)

// SocialConsumer subscribes to the shared social.events.v1 topic and runs the
// connection-request auto-filter (friends-sheets spec §5.1, §9.2 — "P1.4").
// On a ConnectionRequested event it scores the request using trust-safety's
// own data only; abusive requests are pushed to graph-service's filtered queue
// and a ConnectionRequestFiltered event is published back to social.events.v1.
//
// Resilience contract: scoring/DB/HTTP errors are logged and skipped — the
// consumer never crashes and never blocks the topic on a filter decision.
// "When in doubt, don't filter" (fail open).
type SocialConsumer struct {
	store       *postgres.ConnectionFilterStore
	graph       *GraphClient
	kafkaWriter *kafka.Writer
	consumer    *sharedkafka.Consumer
}

// NewSocialConsumer wires the connection-request auto-filter consumer.
//
//   - brokers / topic — Kafka brokers + the social.events.v1 topic.
//   - store           — read-only access to trust.user_trust_state / trust.reports.
//   - graph           — graph-service client used to file the filter decision.
//   - kafkaWriter      — trust-safety's existing social.events.v1 producer, reused
//     to emit ConnectionRequestFiltered.
//   - m               — shared Kafka consumer metrics handle.
func NewSocialConsumer(
	brokers []string,
	topic string,
	store *postgres.ConnectionFilterStore,
	graph *GraphClient,
	kafkaWriter *kafka.Writer,
	m *metrics.KafkaConsumerMetrics,
) *SocialConsumer {
	c := &SocialConsumer{
		store:       store,
		graph:       graph,
		kafkaWriter: kafkaWriter,
	}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:  brokers,
			GroupID:  "trust-safety-connection-filter",
			Topic:    topic,
			DLQTopic: topic + ".dlq",
		},
		nil, // no Redis in trust-safety-service: dedup disabled, handler is idempotent
		m,
		c.handle,
	)
	return c
}

// Start blocks consuming messages until ctx is cancelled.
func (c *SocialConsumer) Start(ctx context.Context) {
	c.consumer.Start(ctx)
}

// Close shuts the underlying consumer down.
func (c *SocialConsumer) Close() error {
	return c.consumer.Close()
}

// handle dispatches on event_type. Only ConnectionRequested is acted on; every
// other event on social.events.v1 is silently ignored.
func (c *SocialConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	if env.EventType != events.ConnectionRequested {
		return nil
	}
	return c.handleConnectionRequested(ctx, env)
}

// handleConnectionRequested scores one connection request and, if it fails the
// heuristic, files the filter decision with graph-service + emits a
// ConnectionRequestFiltered event.
//
// IMPORTANT: this returns nil on every recoverable error. Returning an error
// would make the shared consumer retry and eventually DLQ the message, which
// for a fail-open filter is wrong — a transient DB blip must not block the
// request or land it in the DLQ. Bad payloads are also dropped (nil) since a
// retry would never fix them.
func (c *SocialConsumer) handleConnectionRequested(ctx context.Context, env *events.EventEnvelope) error {
	var p events.ConnectionRequestedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		slog.Warn("connection-filter: bad ConnectionRequested payload, dropping",
			"event_id", env.EventID, "error", err)
		return nil
	}

	senderID, err := uuid.Parse(p.SenderID)
	if err != nil {
		slog.Warn("connection-filter: invalid sender_id, dropping",
			"event_id", env.EventID, "sender_id", p.SenderID, "error", err)
		return nil
	}
	receiverID, err := uuid.Parse(p.ReceiverID)
	if err != nil {
		slog.Warn("connection-filter: invalid receiver_id, dropping",
			"event_id", env.EventID, "receiver_id", p.ReceiverID, "error", err)
		return nil
	}

	reason, ok := c.score(ctx, senderID, receiverID)
	if !ok {
		// Scoring decided not to filter (clean request, or a fail-open skip on a
		// scoring error). Nothing else to do — the request stays visible.
		return nil
	}

	slog.Info("connection-filter: filtering connection request",
		"sender_id", p.SenderID, "receiver_id", p.ReceiverID, "reason", reason)

	// 1. Tell graph-service to move the request to the hidden queue. Log and
	//    continue on failure — we still want to emit the filtered event so
	//    other consumers (analytics, etc.) observe the decision.
	if err := c.graph.FilterConnectionRequest(ctx, p.SenderID, p.ReceiverID); err != nil {
		slog.Error("connection-filter: graph-service filter call failed",
			"sender_id", p.SenderID, "receiver_id", p.ReceiverID, "reason", reason, "error", err)
	}

	// 2. Emit ConnectionRequestFiltered on social.events.v1.
	c.emitFiltered(ctx, p.SenderID, p.ReceiverID, reason)
	return nil
}

// score applies the Phase-1 heuristic and returns the filter reason plus
// ok=true when the request should be filtered. ok=false means leave the
// request visible — that includes the fail-open path: any DB error returns
// ("", false) so a scoring outage never causes false-positive filtering.
//
// Rules, in priority order (friends-sheets spec §5.1):
//  1. Sender has a trust.user_trust_state row with shadowbanned = TRUE
//     -> filter, reason "sender_shadowbanned".
//  2. Else sender's trust_score < FilterTrustScoreThreshold (20)
//     -> filter, reason "low_trust_score".
//     A sender with NO trust-state row is treated as not-shadowbanned and
//     trusted enough (fail open) — it skips rules 1 and 2.
//  3. Else the receiver has previously filed a trust.reports report against
//     the sender (reporter_id = receiver, entity_type='user', entity_id=sender)
//     -> filter, reason "prior_report".
//  4. Otherwise -> not filtered.
func (c *SocialConsumer) score(ctx context.Context, senderID, receiverID uuid.UUID) (string, bool) {
	sig, err := c.store.GetSenderTrustSignal(ctx, senderID)
	if err != nil {
		slog.Error("connection-filter: trust-signal lookup failed, failing open",
			"sender_id", senderID, "error", err)
		return "", false
	}

	if sig.HasState {
		if sig.Shadowbanned {
			return reasonSenderShadowbanned, true
		}
		if sig.TrustScore < FilterTrustScoreThreshold {
			return reasonLowTrustScore, true
		}
	}

	priorReport, err := c.store.HasPriorReportAgainst(ctx, receiverID, senderID)
	if err != nil {
		slog.Error("connection-filter: prior-report lookup failed, failing open",
			"sender_id", senderID, "receiver_id", receiverID, "error", err)
		return "", false
	}
	if priorReport {
		return reasonPriorReport, true
	}

	return "", false
}

// emitFiltered publishes a ConnectionRequestFiltered event to social.events.v1
// via trust-safety's existing producer. Best-effort: a publish failure is
// logged, never fatal.
func (c *SocialConsumer) emitFiltered(ctx context.Context, senderID, receiverID, reason string) {
	if c.kafkaWriter == nil {
		return
	}
	payload := events.ConnectionRequestFilteredPayload{
		SenderID:   senderID,
		ReceiverID: receiverID,
		Reason:     reason,
		FilteredAt: time.Now(),
	}
	pBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("connection-filter: marshal ConnectionRequestFiltered failed", "error", err)
		return
	}
	envelope := events.NewEnvelope(ctx, events.ConnectionRequestFiltered, &senderID, pBytes)
	eBytes, err := json.Marshal(envelope)
	if err != nil {
		slog.Error("connection-filter: marshal envelope failed", "error", err)
		return
	}
	if err := c.kafkaWriter.WriteMessages(ctx, kafka.Message{
		Key:   []byte(receiverID),
		Value: eBytes,
	}); err != nil {
		slog.Error("connection-filter: publish ConnectionRequestFiltered failed",
			"sender_id", senderID, "receiver_id", receiverID, "reason", reason, "error", err)
	}
}
