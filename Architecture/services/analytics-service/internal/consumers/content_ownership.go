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
	if envelope.EventType != events.PostCreated {
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
