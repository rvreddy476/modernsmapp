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
	return NewProducerWithDialer(brokers, topic, nil)
}

func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
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

// PublishPostContentTypeChanged is fired by the MediaTranscodeConsumer
// after a post is reclassified (typically long_video → flick once
// transcode reveals the real duration + dimensions). feed-service
// consumes this and rewrites the matching content_type column on
// home_timeline_by_user + author_timeline_by_author so feed queries
// that filter on content_type don't return stale results.
func (p *Producer) PublishPostContentTypeChanged(ctx context.Context, postID, authorID uuid.UUID, oldType, newType string) error {
	payload := events.PostContentTypeChangedPayload{
		PostID:    postID.String(),
		AuthorID:  authorID.String(),
		OldType:   oldType,
		NewType:   newType,
		ChangedAt: time.Now(),
	}
	return p.publish(ctx, events.PostContentTypeChanged, &authorID, payload)
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

	// Partition key: prefer actorID so all events for the same user
	// land on the same partition and stay ordered (PostCreated →
	// PostUpdated → PostDeleted are processed in order by feed-service).
	// Falls back to EventID for actorless events. Previously every
	// event used a fresh EventID, randomising partitions and breaking
	// per-user ordering guarantees consumers relied on.
	key := []byte(envelope.EventID)
	if actorStr != nil {
		key = []byte(*actorStr)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   key,
		Value: envelopeBytes,
	})
}

// PublishRaw publishes a pre-built EventEnvelope to Kafka (used by outbox worker).
func (p *Producer) PublishRaw(ctx context.Context, envelope events.EventEnvelope) error {
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: envelopeBytes,
	})
}

// PublishUploadDeleted emits an upload.deleted event when a user deletes their upload.
func (p *Producer) PublishUploadDeleted(ctx context.Context, postID, authorID uuid.UUID, contentType string) error {
	payload := events.UploadDeletedPayload{
		PostID:      postID.String(),
		AuthorID:    authorID.String(),
		ContentType: contentType,
		DeletedAt:   time.Now(),
	}
	return p.publish(ctx, events.UploadDeleted, &authorID, payload)
}

// PublishCrosspostRemoved emits a crosspost.removed event.
func (p *Producer) PublishCrosspostRemoved(ctx context.Context, crosspostID, sourcePostID uuid.UUID, sourceModule string, targetPostID uuid.UUID) error {
	payload := events.CrosspostRemovedPayload{
		CrosspostID:  crosspostID.String(),
		SourcePostID: sourcePostID.String(),
		SourceModule: sourceModule,
		TargetPostID: targetPostID.String(),
		RemovedAt:    time.Now(),
	}
	return p.publish(ctx, events.CrosspostRemoved, nil, payload)
}

// PublishPostReposted emits a post.reposted event when a user reposts a post.
func (p *Producer) PublishPostReposted(ctx context.Context, repostID, reposterID, originalPostID, originalAuthorID uuid.UUID, repostType, quoteText, visibility, sourceCtxType, sourceCtxID string) error {
	payload := events.PostRepostedPayload{
		RepostID:          repostID.String(),
		ReposterUserID:    reposterID.String(),
		OriginalPostID:    originalPostID.String(),
		OriginalAuthorID:  originalAuthorID.String(),
		RepostType:        repostType,
		QuoteText:         quoteText,
		Visibility:        visibility,
		SourceContextType: sourceCtxType,
		SourceContextID:   sourceCtxID,
		CreatedAt:         time.Now(),
	}
	return p.publish(ctx, events.EventPostReposted, &reposterID, payload)
}

// PublishPostRepostUndone emits a post.repost_undone event when a user undoes a repost.
func (p *Producer) PublishPostRepostUndone(ctx context.Context, repostID, reposterID, originalPostID, originalAuthorID uuid.UUID, repostType string) error {
	payload := events.PostRepostUndonePayload{
		RepostID:         repostID.String(),
		ReposterUserID:   reposterID.String(),
		OriginalPostID:   originalPostID.String(),
		OriginalAuthorID: originalAuthorID.String(),
		RepostType:       repostType,
		UndoneAt:         time.Now(),
	}
	return p.publish(ctx, events.EventPostRepostUndone, &reposterID, payload)
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
