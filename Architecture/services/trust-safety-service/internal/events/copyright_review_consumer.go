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
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The repeat-uploader rule's input (founder quick wins, 2026-10-24).
//
// post-service's copyright title review publishes
// post.copyright_review.changed on the social topic. A "blocked" transition
// — a reviewer upheld the block, or the system blocked it for a registered
// owner — is recorded as ONE upheld copyright block (trust.copyright_blocks,
// keyed by the review), which the standing answer counts: three within 90
// days pause the author's long-video uploads for 7 days
// (service.LongVideoPause).
//
// A "cleared" transition overturns the block (post-service migration 076:
// the author appealed and a reviewer cleared it): the review's block is
// voided, so it stops counting and the pause lifts on the next standing
// answer when fewer than three remain. A clear of a review that was never
// blocked matches no block and changes nothing; a block of the same review
// after the clear counts once again. "pending" (a hold, or an appeal going
// back to a reviewer) is not ours: the block stands until a reviewer
// clears it.
//
// Durable: a store failure retries the event (RetryForever); a payload that
// names no author, post or review is permanent (DLQ). Events of one review
// share the post's partition key, so they arrive in order; a redelivery is
// a replay of that order, which the store's time guards make harmless.

// copyrightBlockStore is the store slice the consumer writes through.
type copyrightBlockStore interface {
	RecordCopyrightBlock(ctx context.Context, in postgres.CopyrightBlockInput) (bool, error)
	VoidCopyrightBlock(ctx context.Context, in postgres.CopyrightBlockVoidInput) (bool, error)
}

// CopyrightReviewConsumer records upheld title-review blocks and voids the
// ones a reviewer overturns.
type CopyrightReviewConsumer struct {
	store    copyrightBlockStore
	consumer *sharedkafka.Consumer
}

// NewCopyrightReviewConsumer wires the recorder onto the shared consumer
// (no auto-commit, retries, DLQ) in its own group on topic.
func NewCopyrightReviewConsumer(brokers []string, topic string, store copyrightBlockStore, m *metrics.KafkaConsumerMetrics) *CopyrightReviewConsumer {
	c := &CopyrightReviewConsumer{store: store}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      "trust-safety-copyright-review",
			Topic:        topic,
			DLQTopic:     topic + ".dlq",
			RetryForever: true,
		},
		nil, // the store writes are idempotent per review
		m,
		c.handle,
	)
	return c
}

func (c *CopyrightReviewConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *CopyrightReviewConsumer) Close() error              { return c.consumer.Close() }

// copyrightReviewPlan is what one event does: record a block, void one, or
// (both nil) nothing.
type copyrightReviewPlan struct {
	block *postgres.CopyrightBlockInput
	void  *postgres.CopyrightBlockVoidInput
}

// planCopyrightReview turns one envelope into its write. An event that is
// not a title-review blocked or cleared transition plans nothing.
func planCopyrightReview(env *events.EventEnvelope) (copyrightReviewPlan, error) {
	if env == nil || env.EventType != events.EventPostCopyrightReview {
		return copyrightReviewPlan{}, nil
	}
	var p events.PostCopyrightReviewPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return copyrightReviewPlan{}, sharedkafka.Permanent(fmt.Errorf("decode copyright review: %w", err))
	}
	state := strings.TrimSpace(p.State)
	if state != events.CopyrightReviewBlocked && state != events.CopyrightReviewCleared {
		return copyrightReviewPlan{}, nil
	}
	var userID, postID, reviewID uuid.UUID
	var err error
	if userID, err = uuid.Parse(p.AuthorID); err != nil || userID == uuid.Nil {
		return copyrightReviewPlan{}, sharedkafka.Permanent(fmt.Errorf("copyright review %q: invalid author_id", p.ReviewID))
	}
	if postID, err = uuid.Parse(p.PostID); err != nil || postID == uuid.Nil {
		return copyrightReviewPlan{}, sharedkafka.Permanent(fmt.Errorf("copyright review %q: invalid post_id", p.ReviewID))
	}
	if reviewID, err = uuid.Parse(p.ReviewID); err != nil || reviewID == uuid.Nil {
		return copyrightReviewPlan{}, sharedkafka.Permanent(fmt.Errorf("copyright review: invalid review_id"))
	}
	var decidedBy *uuid.UUID
	if p.DecidedBy != "" {
		if by, err := uuid.Parse(p.DecidedBy); err == nil && by != uuid.Nil {
			decidedBy = &by
		}
	}
	at := decisionTime(env, &p)

	if state == events.CopyrightReviewCleared {
		reason := postgres.CopyrightBlockVoidReviewCleared
		if p.Appeal {
			reason = postgres.CopyrightBlockVoidAppealCleared
		}
		return copyrightReviewPlan{void: &postgres.CopyrightBlockVoidInput{
			UserID: userID, ReviewID: reviewID, ClearedAt: at, VoidedBy: decidedBy, Reason: reason,
		}}, nil
	}
	return copyrightReviewPlan{block: &postgres.CopyrightBlockInput{
		UserID: userID, PostID: postID, ReviewID: reviewID, MatchedTitle: p.MatchedTitle,
		DecidedBy: decidedBy, BlockedAt: at,
	}}, nil
}

// decisionTime is when the transition was decided: post-service's
// decided_at (set on every blocked and cleared), else when the event says
// it happened. The block's time and the clear's time come from the same
// field, so the store's "older than the clear" guard compares like with
// like.
func decisionTime(env *events.EventEnvelope, p *events.PostCopyrightReviewPayload) time.Time {
	switch {
	case p.DecidedAt != nil && !p.DecidedAt.IsZero():
		return p.DecidedAt.UTC()
	case !p.OccurredAt.IsZero():
		return p.OccurredAt.UTC()
	case !env.OccurredAt.IsZero():
		return env.OccurredAt.UTC()
	default:
		return time.Now().UTC()
	}
}

func (c *CopyrightReviewConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	plan, err := planCopyrightReview(env)
	if err != nil {
		return err
	}
	switch {
	case plan.block != nil:
		in := plan.block
		created, err := c.store.RecordCopyrightBlock(ctx, *in)
		if err != nil {
			return err // retried: the block must not be lost
		}
		if created {
			slog.Info("copyright block recorded", "user_id", in.UserID, "post_id", in.PostID, "review_id", in.ReviewID)
		}
	case plan.void != nil:
		in := plan.void
		voided, err := c.store.VoidCopyrightBlock(ctx, *in)
		if err != nil {
			return err // retried: an overturned block must not keep counting
		}
		if voided {
			slog.Info("copyright block voided", "user_id", in.UserID, "review_id", in.ReviewID, "reason", in.Reason)
		}
	}
	return nil
}
