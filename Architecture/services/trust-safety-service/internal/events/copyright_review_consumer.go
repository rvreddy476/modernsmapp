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
// — a reviewer upheld the block — is recorded as ONE upheld copyright block
// (trust.copyright_blocks, keyed by the review), which the standing answer
// counts: three within 90 days pause the author's long-video uploads for 7
// days (service.LongVideoPause). Every other state is not ours and is
// skipped. Durable: a store failure retries the record (RetryForever); a
// payload that names no author, post or review is permanent (DLQ).

// copyrightBlockRecorder is the store slice the consumer writes through.
type copyrightBlockRecorder interface {
	RecordCopyrightBlock(ctx context.Context, in postgres.CopyrightBlockInput) (bool, error)
}

// CopyrightReviewConsumer records upheld title-review blocks.
type CopyrightReviewConsumer struct {
	store    copyrightBlockRecorder
	consumer *sharedkafka.Consumer
}

// NewCopyrightReviewConsumer wires the recorder onto the shared consumer
// (no auto-commit, retries, DLQ) in its own group on topic.
func NewCopyrightReviewConsumer(brokers []string, topic string, store copyrightBlockRecorder, m *metrics.KafkaConsumerMetrics) *CopyrightReviewConsumer {
	c := &CopyrightReviewConsumer{store: store}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      "trust-safety-copyright-review",
			Topic:        topic,
			DLQTopic:     topic + ".dlq",
			RetryForever: true,
		},
		nil, // the store write is idempotent per review
		m,
		c.handle,
	)
	return c
}

func (c *CopyrightReviewConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *CopyrightReviewConsumer) Close() error              { return c.consumer.Close() }

// planCopyrightBlock turns one envelope into the block it records. ok is
// false for an event that is not an upheld block (another type, another
// state).
func planCopyrightBlock(env *events.EventEnvelope) (postgres.CopyrightBlockInput, bool, error) {
	if env == nil || env.EventType != events.EventPostCopyrightReview {
		return postgres.CopyrightBlockInput{}, false, nil
	}
	var p events.PostCopyrightReviewPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return postgres.CopyrightBlockInput{}, false, sharedkafka.Permanent(fmt.Errorf("decode copyright review: %w", err))
	}
	if strings.TrimSpace(p.State) != events.CopyrightReviewBlocked {
		return postgres.CopyrightBlockInput{}, false, nil
	}
	in := postgres.CopyrightBlockInput{MatchedTitle: p.MatchedTitle}
	var err error
	if in.UserID, err = uuid.Parse(p.AuthorID); err != nil || in.UserID == uuid.Nil {
		return in, false, sharedkafka.Permanent(fmt.Errorf("copyright review %q: invalid author_id", p.ReviewID))
	}
	if in.PostID, err = uuid.Parse(p.PostID); err != nil || in.PostID == uuid.Nil {
		return in, false, sharedkafka.Permanent(fmt.Errorf("copyright review %q: invalid post_id", p.ReviewID))
	}
	if in.ReviewID, err = uuid.Parse(p.ReviewID); err != nil || in.ReviewID == uuid.Nil {
		return in, false, sharedkafka.Permanent(fmt.Errorf("copyright review: invalid review_id"))
	}
	if p.DecidedBy != "" {
		if by, err := uuid.Parse(p.DecidedBy); err == nil && by != uuid.Nil {
			in.DecidedBy = &by
		}
	}
	switch {
	case p.DecidedAt != nil && !p.DecidedAt.IsZero():
		in.BlockedAt = p.DecidedAt.UTC()
	case !p.OccurredAt.IsZero():
		in.BlockedAt = p.OccurredAt.UTC()
	case !env.OccurredAt.IsZero():
		in.BlockedAt = env.OccurredAt.UTC()
	default:
		in.BlockedAt = time.Now().UTC()
	}
	return in, true, nil
}

func (c *CopyrightReviewConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	in, ok, err := planCopyrightBlock(env)
	if err != nil || !ok {
		return err
	}
	created, err := c.store.RecordCopyrightBlock(ctx, in)
	if err != nil {
		return err // retried: the block must not be lost
	}
	if created {
		slog.Info("copyright block recorded", "user_id", in.UserID, "post_id", in.PostID, "review_id", in.ReviewID)
	}
	return nil
}
