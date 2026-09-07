package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type ownershipStore interface {
	UpsertContentOwnership(context.Context, pgstore.ContentOwnership) error
	UpdateContentType(ctx context.Context, contentID, creatorID uuid.UUID, contentType string) (bool, error)
}

type permanentOwnershipError struct{ err error }

func (e permanentOwnershipError) Error() string { return e.err.Error() }
func (e permanentOwnershipError) Unwrap() error { return e.err }

type ContentOwnershipConsumer struct {
	store ownershipStore
}

func NewContentOwnershipConsumer(store ownershipStore) *ContentOwnershipConsumer {
	return &ContentOwnershipConsumer{store: store}
}

// Start projects immutable content ownership. A durable failure keeps the
// fetched record in flight and stalls the partition; only syntactically
// undecodable or permanently invalid PostCreated records advance.
func (c *ContentOwnershipConsumer) Start(ctx context.Context, brokers []string, topic string, dialer *kafka.Dialer) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, GroupID: "analytics-content-ownership", Topic: topic,
		MinBytes: 1, MaxBytes: 10e6, Dialer: dialer,
	})
	defer reader.Close()

	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("ownership projection fetch failed", "error", err)
			continue
		}
		for {
			err = c.apply(ctx, message.Value)
			var permanent permanentOwnershipError
			if err == nil || errors.As(err, &permanent) {
				if commitErr := reader.CommitMessages(ctx, message); commitErr != nil {
					slog.Error("ownership projection commit failed", "error", commitErr)
					continue
				}
				break
			}
			slog.Error("ownership projection durable apply failed; retrying same record", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

func (c *ContentOwnershipConsumer) apply(ctx context.Context, value []byte) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(value, &envelope); err != nil {
		return permanentOwnershipError{err}
	}
	switch envelope.EventType {
	case events.PostCreated:
		// fall through to the projection below
	case events.PostContentTypeChanged:
		return c.applyReclassification(ctx, envelope)
	default:
		return nil
	}
	var payload events.PostCreatedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentOwnershipError{err}
	}
	contentID, err := uuid.Parse(payload.PostID)
	if err != nil || contentID == uuid.Nil {
		return permanentOwnershipError{errors.New("PostCreated has invalid post_id")}
	}
	creatorID, err := uuid.Parse(payload.AuthorID)
	if err != nil || creatorID == uuid.Nil {
		return permanentOwnershipError{errors.New("PostCreated has invalid author_id")}
	}
	contentType := payload.ContentType
	if contentType == "" {
		contentType = "post"
	}
	createdAt := payload.CreatedAt
	if createdAt.IsZero() {
		createdAt = envelope.OccurredAt
	}
	if createdAt.IsZero() {
		return permanentOwnershipError{errors.New("PostCreated has no creation time")}
	}
	return c.store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{
		ContentID: contentID, CreatorID: creatorID,
		ContentType: contentType, CreatedAt: createdAt,
	})
}

// applyReclassification keeps the ownership projection's content_type in
// step with post-service after the transcode pipeline reclassifies a
// video. shared/postclassify is the one rule that decides flick vs
// long_video; post-service applies it and announces the result, and this
// is analytics' side of that contract.
//
// It matters twice over, because content_type is not a label here:
// model.IsDisplayView picks the display-view bar from it, and
// monetization-service resolves the per-content-type RPM rate from the
// aggregates it ends up on — a flick and a long_video are 300 and 5000
// paise per 1000 views respectively. A post that uploaded as a plain
// video and turned out to be a flick would otherwise be counted and paid
// as long-form for the rest of its life.
//
// A missing row is permanent, not retryable: PostCreated is produced to
// the same topic under the same author key, so it has already been seen.
// Nothing to update means the create was rejected (an ownership
// conflict), and replaying this record forever would stall the partition.
func (c *ContentOwnershipConsumer) applyReclassification(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.PostContentTypeChangedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentOwnershipError{err}
	}
	contentID, err := uuid.Parse(payload.PostID)
	if err != nil || contentID == uuid.Nil {
		return permanentOwnershipError{errors.New("PostContentTypeChanged has invalid post_id")}
	}
	creatorID, err := uuid.Parse(payload.AuthorID)
	if err != nil || creatorID == uuid.Nil {
		return permanentOwnershipError{errors.New("PostContentTypeChanged has invalid author_id")}
	}
	if payload.NewType == "" {
		return permanentOwnershipError{errors.New("PostContentTypeChanged has no new_type")}
	}
	applied, err := c.store.UpdateContentType(ctx, contentID, creatorID, payload.NewType)
	if err != nil {
		return err
	}
	if !applied {
		slog.Warn("reclassification has no ownership row to correct",
			"content_id", contentID.String(), "new_type", payload.NewType)
		return nil
	}
	slog.Info("ownership content_type reclassified",
		"content_id", contentID.String(),
		"old_type", payload.OldType, "new_type", payload.NewType)
	return nil
}
