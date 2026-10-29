package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader *kafka.Reader
	store  *store.Store
	rdb    *redis.Client
	// eraser is the account erasure (store.AnonymizeUser); nil means store.
	eraser UserEraser
	// media releases what erased questions and answers held; nil releases
	// nothing (WithMediaRelease).
	media MediaReleaser
}

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

func (c *Consumer) Start(ctx context.Context) {
	slog.Info("qa-service consumer listening on platform-events")
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("consumer shutting down")
				return
			}
			slog.Error("consumer read error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if err := c.processMessage(ctx, m); err != nil {
			slog.Warn("failed to process message", "error", err)
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
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
		return err
	}

	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return err
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
