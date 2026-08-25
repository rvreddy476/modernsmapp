package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
)

// Module 4 M4-P0-4 — applying a story moderation decision.
//
// post-service owns story publication state. trust-safety-service produces the
// decision and the evidence; this applies it. Keeping the write on one side
// means there is exactly one answer to "is this story published", rather than
// two services racing to set it.
//
// WHAT MAKES A LATE OR REPEATED DECISION SAFE
//
// ApplyStoryModerationDecision matches on content_revision AND requires the
// story to still be pending/manual_review. So:
//
//   - a decision for a revision that is no longer current cannot approve
//     content that changed after it was evaluated;
//   - a redelivered decision matches no row the second time and is a no-op;
//   - a decision that arrives after a human already ruled cannot overwrite it.
//
// All three resolve to "applied = false, error = nil", which is a SUCCESS for
// the consumer — the desired state is already in place, so the offset may
// advance. Treating it as failure would retry forever on a decision that can
// never apply.

// StoryModerationApplier consumes decisions and applies them.
type StoryModerationApplier struct {
	store    *postgres.Store
	consumer *sharedkafka.Consumer
	verifier *moderationcap.Verifier
}

func NewStoryModerationApplier(
	brokers []string,
	topic string,
	store *postgres.Store,
	verifier *moderationcap.Verifier,
	m *metrics.KafkaConsumerMetrics,
) *StoryModerationApplier {
	a := &StoryModerationApplier{store: store, verifier: verifier}
	a.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      "post-service-story-moderation-applier",
			Topic:        topic,
			DLQTopic:     topic + ".dlq",
			RetryForever: true,
		},
		nil, // the apply is idempotent by revision; Redis dedup would add nothing
		m,
		a.handle,
	)
	return a
}

func (a *StoryModerationApplier) Start(ctx context.Context) { a.consumer.Start(ctx) }
func (a *StoryModerationApplier) Close() error              { return a.consumer.Close() }

func (a *StoryModerationApplier) handle(ctx context.Context, env *events.EventEnvelope) error {
	if env.EventType != events.StoryModerationDecided {
		return nil
	}

	var p events.StoryModerationDecidedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return sharedkafka.Permanent(fmt.Errorf("decode story decision: %w", err))
	}
	storyID, err := uuid.Parse(p.StoryID)
	if err != nil {
		return sharedkafka.Permanent(fmt.Errorf("story decision has unparseable story_id %q: %w", p.StoryID, err))
	}
	if p.ContentRevision <= 0 {
		// A decision with no revision cannot be matched to content, so it
		// cannot be safely applied to anything.
		return sharedkafka.Permanent(fmt.Errorf("story decision for %s carries no content revision", p.StoryID))
	}
	if a.verifier == nil {
		return sharedkafka.Permanent(fmt.Errorf("story decision verifier is not configured"))
	}
	if err := a.verifier.Verify(moderationcap.Claims{
		Issuer:          p.Issuer,
		Purpose:         p.Purpose,
		SubjectID:       p.StoryID,
		ContentRevision: p.ContentRevision,
		Decision:        p.Decision,
		Reason:          p.Reason,
		DecisionID:      p.DecisionID,
		PolicyVersion:   p.PolicyVersion,
		IssuedAtUnix:    p.IssuedAtUnix,
		ExpiresAtUnix:   p.ExpiresAtUnix,
	}, p.Capability); err != nil {
		return sharedkafka.Permanent(fmt.Errorf("story decision capability rejected: %w", err))
	}

	// The store rejects any state outside the closed set, so a forged or
	// corrupted decision cannot invent one.
	applied, err := a.store.ApplyStoryModerationDecision(
		ctx, storyID, p.ContentRevision, p.Decision, p.DecisionID, p.Reason, p.PolicyVersion)
	if err != nil {
		// Returned, not logged: the offset must not advance past a decision
		// that failed to persist, or the story stays pending forever with
		// nothing scheduled to retry it.
		return fmt.Errorf("apply story decision %s: %w", p.StoryID, err)
	}

	if !applied {
		// Stale revision, replay, or already terminal. Nothing to do, and
		// nothing wrong — see the header.
		slog.Info("story moderation decision did not apply (stale, replayed, or already terminal)",
			"story_id", p.StoryID, "revision", p.ContentRevision, "decision", p.Decision)
		return nil
	}
	slog.Info("story moderation decision applied",
		"story_id", p.StoryID, "revision", p.ContentRevision,
		"decision", p.Decision, "policy_version", p.PolicyVersion)
	return nil
}
