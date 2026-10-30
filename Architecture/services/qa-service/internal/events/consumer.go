package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader messageReader
	store  *store.Store
	rdb    *redis.Client
	// eraser is the account erasure (store.AnonymizeUser); nil means store.
	eraser UserEraser
	// media releases what erased questions and answers held; nil releases
	// nothing (WithMediaRelease).
	media MediaReleaser
	// deadLetters parks an event that keeps failing; nil means store.
	deadLetters DeadLetterStore
	// maxAttempts is how many times one event is tried before it is parked.
	maxAttempts int
	// backoff is the first retry delay (doubled up to maxBackoff).
	backoff    time.Duration
	maxBackoff time.Duration
}

// messageReader is the part of *kafka.Reader the consumer uses.
type messageReader interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

// DeadLetterStore parks a platform event the consumer gave up on
// (store.RecordPlatformEventDeadLetter).
type DeadLetterStore interface {
	RecordPlatformEventDeadLetter(ctx context.Context, topic string, partition int, offset int64, eventType string, value []byte, attempts int, lastErr string) error
}

// Retry policy for a failing platform event (audit M8).
const (
	DefaultMaxAttempts = 10
	defaultBackoff     = time.Second
	defaultMaxBackoff  = time.Minute
)

// UserEraser is the store's account erasure.
type UserEraser interface {
	AnonymizeUser(ctx context.Context, userID uuid.UUID) (store.Erased, error)
}

// MediaReleaser releases the media claims of removed questions and answers
// (service.Service.ReleaseRemovedMedia). Best effort: it logs, never fails.
type MediaReleaser interface {
	ReleaseRemovedMedia(ctx context.Context, questionIDs, answerIDs []uuid.UUID)
}

// WithMediaRelease installs the release of an erased account's media claims.
func (c *Consumer) WithMediaRelease(r MediaReleaser) *Consumer {
	c.media = r
	return c
}

// WithEraser replaces the store's erasure (tests).
func (c *Consumer) WithEraser(e UserEraser) *Consumer {
	c.eraser = e
	return c
}

func NewConsumer(brokers []string, groupID string, s *store.Store, rdb *redis.Client) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, s, rdb, nil)
}

func NewConsumerWithDialer(brokers []string, groupID string, s *store.Store, rdb *redis.Client, dialer *kafka.Dialer) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    "platform-events",
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &Consumer{reader: reader, store: s, rdb: rdb}
}

// Start consumes platform-events until ctx ends.
//
// Audit M8: it used ReadMessage, which commits the offset before the event
// is processed, and only logged a failure, so an account erasure or a
// community ban that met a Postgres failover was lost for good. Now each
// event is fetched, applied (retried with back-off while it fails), and
// only then committed. An event that still fails after maxAttempts is
// parked in qa_platform_event_dead_letters and then committed, so one
// poison message cannot hold the partition forever; if even parking fails
// (the database is down), the event keeps being retried and the offset
// stays where it is. Every handler is idempotent, so a redelivery after a
// crash between apply and commit is harmless.
func (c *Consumer) Start(ctx context.Context) {
	slog.Info("qa-service consumer listening on platform-events")
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("consumer shutting down")
				return
			}
			slog.Error("consumer fetch error", "error", err)
			if !sleepCtx(ctx, 2*time.Second) {
				return
			}
			continue
		}
		if !c.handleUntilSettled(ctx, m) {
			return // shutting down; the offset stays uncommitted
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			// The event is applied; a redelivery re-applies it harmlessly.
			slog.Error("consumer commit error", "error", err, "partition", m.Partition, "offset", m.Offset)
		}
	}
}

// handleUntilSettled applies m until it succeeds, is known to be
// unprocessable, or is parked as a dead letter. It returns false only when
// ctx ends first (the caller must not commit).
func (c *Consumer) handleUntilSettled(ctx context.Context, m kafka.Message) bool {
	maxAttempts := c.maxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	delay, maxDelay := c.backoff, c.maxBackoff
	if delay <= 0 {
		delay = defaultBackoff
	}
	if maxDelay <= 0 {
		maxDelay = defaultMaxBackoff
	}
	for attempt := 1; ; attempt++ {
		err := c.processMessage(ctx, m)
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		var bad permanentError
		if errors.As(err, &bad) {
			// Undecodable or malformed: no retry can apply it.
			slog.Warn("qa platform event is malformed; skipping", "error", err, "partition", m.Partition, "offset", m.Offset)
			return true
		}
		slog.Warn("qa platform event failed; holding the offset",
			"error", err, "attempt", attempt, "partition", m.Partition, "offset", m.Offset)
		if attempt >= maxAttempts {
			dlErr := c.deadLetter(ctx, m, attempt, err)
			if dlErr == nil {
				slog.Error("qa platform event parked as a dead letter after its retries",
					"partition", m.Partition, "offset", m.Offset, "attempts", attempt)
				return true
			}
			slog.Error("qa platform event could not be parked; retrying", "error", dlErr)
		}
		if !sleepCtx(ctx, delay) {
			return false
		}
		if delay *= 2; delay > maxDelay {
			delay = maxDelay
		}
	}
}

func (c *Consumer) deadLetter(ctx context.Context, m kafka.Message, attempts int, cause error) error {
	var dl DeadLetterStore = c.deadLetters
	if dl == nil {
		if c.store == nil {
			return errors.New("no dead-letter store")
		}
		dl = c.store
	}
	var envelope events.EventEnvelope
	_ = json.Unmarshal(m.Value, &envelope)
	return dl.RecordPlatformEventDeadLetter(ctx, m.Topic, m.Partition, m.Offset, envelope.EventType, m.Value, attempts, cause.Error())
}

// permanentError marks an event no retry can apply (bad JSON, bad ids).
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// permanent wraps err as unprocessable.
func permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// sleepCtx waits d, or reports false when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return permanent(err)
	}

	switch envelope.EventType {
	case events.EventUserDeletionRequested:
		return c.handleUserDeletionRequested(ctx, envelope)
	case events.EventCommunityCreated:
		return c.handleCommunityCreated(ctx, envelope)
	case events.EventCommunityUpdated:
		return c.handleCommunityUpdated(ctx, envelope)
	case events.EventCommunityDeleted:
		return c.handleCommunityDeleted(ctx, envelope)
	case events.EventCommunityMemberJoined:
		return c.handleCommunityMemberJoined(ctx, envelope)
	case events.EventCommunityMemberLeft:
		return c.handleCommunityMemberLeft(ctx, envelope)
	case events.EventCommunityMemberBanned:
		return c.handleCommunityMemberBanned(ctx, envelope)
	case events.EventCommunityMemberRoleChanged:
		return c.handleCommunityMemberRoleChanged(ctx, envelope)
	}

	return nil
}

func (c *Consumer) handleUserDeletionRequested(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserDeletionRequestedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanent(err)
	}

	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return permanent(err)
	}

	slog.Info("processing GDPR deletion for user in qa-service", "user_id", userID)
	return c.eraseUser(ctx, userID)
}

// eraseUser anonymises the account, then releases the media claims of the
// questions and answers it deleted (and of the answers under those
// questions), so the erased user's media is not pinned until someone runs the
// reconcile tool. The release never fails the erasure.
func (c *Consumer) eraseUser(ctx context.Context, userID uuid.UUID) error {
	var eraser UserEraser = c.eraser
	if eraser == nil {
		eraser = c.store
	}
	erased, err := eraser.AnonymizeUser(ctx, userID)
	if err != nil {
		slog.Warn("failed to anonymize user in qa-service", "user_id", userID, "error", err)
		return err
	}
	if c.media != nil {
		c.media.ReleaseRemovedMedia(ctx, erased.QuestionIDs, erased.AnswerIDs)
	} else if len(erased.QuestionIDs)+len(erased.AnswerIDs) > 0 {
		slog.Warn("qa-service: erased content's media claims not released (no releaser); run the reconcile tool (media-claims -apply)",
			"user_id", userID, "questions", len(erased.QuestionIDs), "answers", len(erased.AnswerIDs))
	}

	slog.Info("GDPR deletion completed for user in qa-service", "user_id", userID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
