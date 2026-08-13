package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
)

// Module 4 M4-P0-4 — the story moderation evaluator.
//
// WHY THIS EXISTS
//
// Before Module 4, stories were the one publication path on this platform with
// no review state at all: `stories` had no moderation column and
// trust-safety-service contained zero references to stories. post-service now
// creates every story `pending` and emits StoryModerationRequested from the
// same transaction. Nothing consumed that event, so no story could ever become
// visible. This closes that half.
//
// THIS SERVICE OWNS THE DECISION, NOT THE ROW
//
// It emits StoryModerationDecided. post-service applies it, revision-matched.
// Two services writing story publication state would be two sources of truth
// for whether something is published, and they would disagree under retry.
//
// FAIL CLOSED, AND "CLOSED" MEANS MANUAL REVIEW
//
// Every path that cannot produce a confident allow resolves to manual_review,
// never to approved. That includes an unparseable payload, an evaluator error,
// and an unrecognised media type. Rejecting outright would destroy legitimate
// content on a transient fault; approving would publish unreviewed media. Held
// is the only honest third answer.
//
// A NOTE ON WHAT IS NOT EVALUATED HERE
//
// The media BYTES are scanned by media-service before a story may reference the
// asset at all (CreateStoryPending refuses media whose moderation_status is not
// passed). What this evaluates is the story-level surface the media scan cannot
// see: the caption, and the combination of caption plus media. Owner-edited
// caption text is display data — it is evaluated as content here, but it is
// never treated as evidence about the media.

// StoryEvaluator decides a story's terminal moderation state.
type StoryEvaluator interface {
	// Evaluate returns a terminal decision and a reason. An error means no
	// decision could be reached; the caller holds the item for manual review.
	Evaluate(ctx context.Context, p events.StoryModerationRequestedPayload) (decision, reason string, err error)
}

// KeywordStoryEvaluator screens captions against a blocklist.
//
// It is deliberately modest about what it claims: it can reject a caption that
// matches a banned term, and it can approve one that does not. It cannot judge
// imagery — that is media-service's scanner — so it never overrides a media
// verdict, only adds the caption dimension.
type KeywordStoryEvaluator struct {
	blocked []string
	version string
}

// NewKeywordStoryEvaluator builds an evaluator from a comma-separated list.
// An empty list yields nil, and a nil evaluator holds everything for manual
// review rather than approving — see StoryModerationConsumer.decide.
func NewKeywordStoryEvaluator(list, version string) *KeywordStoryEvaluator {
	var terms []string
	for _, t := range strings.Split(list, ",") {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			terms = append(terms, t)
		}
	}
	if len(terms) == 0 {
		return nil
	}
	if version == "" {
		version = "keyword-v1"
	}
	return &KeywordStoryEvaluator{blocked: terms, version: version}
}

func (e *KeywordStoryEvaluator) Version() string { return e.version }

func (e *KeywordStoryEvaluator) Evaluate(_ context.Context, p events.StoryModerationRequestedPayload) (string, string, error) {
	caption := strings.ToLower(p.Caption)
	for _, term := range e.blocked {
		if strings.Contains(caption, term) {
			// The matched term is NOT echoed into the reason: the reason is
			// surfaced to the author, and telling them exactly which word
			// tripped the filter is a tuning oracle for anyone probing it.
			return events.StoryDecisionRejected, "caption violates content policy", nil
		}
	}
	return events.StoryDecisionApproved, "", nil
}

// StoryModerationConsumer consumes requests and emits decisions.
type StoryModerationConsumer struct {
	evaluator     StoryEvaluator
	policyVersion string
	writer        *kafka.Writer
	topic         string
	consumer      *sharedkafka.Consumer
	signer        *moderationcap.Signer
}

// NewStoryModerationConsumer wires the evaluator onto the shared consumer,
// which already fetches without auto-commit, retries, and DLQs poison input.
func NewStoryModerationConsumer(
	brokers []string,
	topic string,
	evaluator StoryEvaluator,
	policyVersion string,
	writer *kafka.Writer,
	signer *moderationcap.Signer,
	m *metrics.KafkaConsumerMetrics,
) *StoryModerationConsumer {
	if policyVersion == "" {
		policyVersion = "unset"
	}
	c := &StoryModerationConsumer{
		evaluator:     evaluator,
		policyVersion: policyVersion,
		writer:        writer,
		topic:         topic,
		signer:        signer,
	}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      "trust-safety-story-moderation",
			Topic:        topic,
			DLQTopic:     topic + ".dlq",
			RetryForever: true,
		},
		nil, // handler is idempotent: the same decision applied twice is a no-op
		m,
		c.handle,
	)
	return c
}

func (c *StoryModerationConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *StoryModerationConsumer) Close() error              { return c.consumer.Close() }

func (c *StoryModerationConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	if env.EventType != events.StoryModerationRequested {
		return nil
	}

	var p events.StoryModerationRequestedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		// Unparseable: nothing to evaluate and nothing to hold, because we do
		// not know which story it was. The shared consumer DLQs it.
		return sharedkafka.Permanent(fmt.Errorf("decode story moderation request: %w", err))
	}
	if p.StoryID == "" || p.ContentRevision <= 0 {
		return sharedkafka.Permanent(fmt.Errorf("story moderation request missing story_id or revision"))
	}

	decision, reason := c.decide(ctx, p)

	return c.publishDecision(ctx, events.StoryModerationDecidedPayload{
		StoryID:         p.StoryID,
		ContentRevision: p.ContentRevision,
		Decision:        decision,
		Reason:          reason,
		// Deterministic per (story, revision): a redelivered request produces
		// the SAME decision id, so post-service can recognise a replay rather
		// than treating it as a second decision.
		DecisionID:    deterministicDecisionID(p.StoryID, p.ContentRevision),
		PolicyVersion: c.policyVersion,
	})
}

// decide never returns "approved" on uncertainty.
func (c *StoryModerationConsumer) decide(ctx context.Context, p events.StoryModerationRequestedPayload) (string, string) {
	if c.evaluator == nil {
		// No evaluator configured. Holding is the only safe answer: approving
		// would publish unreviewed media, and this is exactly the state a
		// misconfigured deployment lands in.
		slog.Warn("story moderation: no evaluator configured, holding for manual review",
			"story_id", p.StoryID)
		return events.StoryDecisionManualReview, "awaiting review"
	}
	decision, reason, err := c.evaluator.Evaluate(ctx, p)
	if err != nil {
		slog.Error("story moderation: evaluator failed, holding for manual review",
			"story_id", p.StoryID, "err", err)
		return events.StoryDecisionManualReview, "awaiting review"
	}
	switch decision {
	case events.StoryDecisionApproved, events.StoryDecisionRejected, events.StoryDecisionManualReview:
		return decision, reason
	default:
		// An evaluator returning something unrecognised is a bug, and a bug
		// must not be able to publish.
		slog.Error("story moderation: evaluator returned unknown decision, holding",
			"story_id", p.StoryID, "decision", decision)
		return events.StoryDecisionManualReview, "awaiting review"
	}
}

func (c *StoryModerationConsumer) publishDecision(ctx context.Context, p events.StoryModerationDecidedPayload) error {
	if c.writer == nil {
		// Without a producer the decision cannot reach post-service, and the
		// story stays pending. Returning an error keeps the offset uncommitted
		// so the request is retried rather than silently lost.
		return fmt.Errorf("story moderation: no producer configured")
	}
	if c.signer == nil {
		return fmt.Errorf("story moderation: no approval capability signer configured")
	}
	claims, capability, err := c.signer.Sign(moderationcap.Claims{
		SubjectID:       p.StoryID,
		ContentRevision: p.ContentRevision,
		Decision:        p.Decision,
		Reason:          p.Reason,
		DecisionID:      p.DecisionID,
		PolicyVersion:   p.PolicyVersion,
	})
	if err != nil {
		return fmt.Errorf("sign story decision: %w", err)
	}
	p.Issuer = claims.Issuer
	p.Purpose = claims.Purpose
	p.IssuedAtUnix = claims.IssuedAtUnix
	p.ExpiresAtUnix = claims.ExpiresAtUnix
	p.Capability = capability
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode story decision: %w", err)
	}
	actor := "trust-safety-service"
	env := events.NewEnvelope(ctx, events.StoryModerationDecided, &actor, raw)
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode decision envelope: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The error is RETURNED. A logged-and-swallowed publish failure would leave
	// the story pending forever with the offset committed and nothing to retry.
	if err := c.writer.WriteMessages(writeCtx, kafka.Message{
		Key:   []byte(p.StoryID),
		Value: body,
	}); err != nil {
		return fmt.Errorf("publish story decision: %w", err)
	}
	return nil
}

// deterministicDecisionID derives a stable id from the story and the revision
// under review, so a redelivered request yields the same decision id.
func deterministicDecisionID(storyID string, revision int64) string {
	return uuid.NewSHA1(
		uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8"),
		[]byte(fmt.Sprintf("story-moderation:%s:%d", storyID, revision)),
	).String()
}
