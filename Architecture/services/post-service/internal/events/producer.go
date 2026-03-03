package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string) *Producer {
	w := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}
	return &Producer{writer: w}
}

func (p *Producer) PublishPostCreated(ctx context.Context, postID, authorID uuid.UUID, text, visibility, contentType string, durationSeconds int) error {
	payload := events.PostCreatedPayload{
		PostID:          postID.String(),
		AuthorID:        authorID.String(),
		Text:            text,
		Visibility:      visibility,
		ContentType:     contentType,
		DurationSeconds: durationSeconds,
		CreatedAt:       time.Now(),
	}
	return p.publish(ctx, events.PostCreated, &authorID, payload)
}

func (p *Producer) PublishPostReacted(ctx context.Context, postID, postAuthorID, reactorID uuid.UUID, reactType string) error {
	payload := events.PostReactedPayload{
		PostID:       postID.String(),
		PostAuthorID: postAuthorID.String(),
		ReactorID:    reactorID.String(),
		ReactType:    reactType,
		CreatedAt:    time.Now(),
	}
	return p.publish(ctx, events.PostReacted, &reactorID, payload)
}

func (p *Producer) PublishCommentReacted(ctx context.Context, commentID, postID, commentAuthorID, reactorID uuid.UUID, reactType string) error {
	payload := events.CommentReactedPayload{
		CommentID:       commentID.String(),
		PostID:          postID.String(),
		CommentAuthorID: commentAuthorID.String(),
		ReactorID:       reactorID.String(),
		ReactType:       reactType,
		CreatedAt:       time.Now(),
	}
	return p.publish(ctx, events.CommentReacted, &reactorID, payload)
}

func (p *Producer) PublishCommentCreated(ctx context.Context, commentID, postID, postAuthorID, authorID uuid.UUID, text string) error {
	payload := events.CommentCreatedPayload{
		CommentID:    commentID.String(),
		PostID:       postID.String(),
		PostAuthorID: postAuthorID.String(),
		AuthorID:     authorID.String(),
		Text:         text,
		CreatedAt:    time.Now(),
	}
	return p.publish(ctx, events.CommentCreated, &authorID, payload)
}

func (p *Producer) PublishSpamDetected(ctx context.Context, userID uuid.UUID, reason string, score float64) error {
	payload := events.SpamDetectedPayload{
		UserID: userID.String(),
		Reason: reason,
		Score:  score,
	}
	return p.publish(ctx, events.EventSpamDetected, &userID, payload)
}

func (p *Producer) PublishStoryCreated(ctx context.Context, storyID, authorID uuid.UUID, mediaType string) error {
	payload := events.StoryCreatedPayload{
		StoryID:   storyID.String(),
		AuthorID:  authorID.String(),
		MediaType: mediaType,
		CreatedAt: time.Now(),
	}
	return p.publish(ctx, events.StoryCreated, &authorID, payload)
}

// PublishUserMentioned emits a user.mentioned event for a @mention in a post.
func (p *Producer) PublishUserMentioned(ctx context.Context, mentionedUserID, authorID uuid.UUID, postID string) error {
	payload := events.UserMentionedPayload{
		MentionedUserID: mentionedUserID.String(),
		AuthorID:        authorID.String(),
		PostID:          postID,
		OccurredAt:      time.Now(),
	}
	return p.publish(ctx, events.EventUserMentioned, &authorID, payload)
}

func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	var actorStr *string
	if actorID != nil {
		s := actorID.String()
		actorStr = &s
	}

	envelope := events.NewEnvelope(ctx, eventType, actorStr, payloadBytes)

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: envelopeBytes,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
