package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader        *kafka.Reader
	service       *service.Service
	rdb           *redis.Client
	timelineStore *scylla.TimelineStore
}

func NewConsumer(brokers []string, groupID string, topic string, svc *service.Service, rdb *redis.Client, ts *scylla.TimelineStore) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, topic, svc, rdb, ts, nil)
}

func NewConsumerWithDialer(brokers []string, groupID string, topic string, svc *service.Service, rdb *redis.Client, ts *scylla.TimelineStore, dialer *kafka.Dialer) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
		Dialer:   dialer,
	})
	return &Consumer{reader: reader, service: svc, rdb: rdb, timelineStore: ts}
}

func (c *Consumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("Consumer error: %v\n", err)
			break
		}

		if err := c.processMessage(ctx, m); err != nil {
			log.Printf("Failed to process message: %v\n", err)
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}

	switch envelope.EventType {
	case events.PostCreated:
		return c.handlePostCreated(ctx, envelope)

	case events.PostReacted:
		return c.handlePostReacted(ctx, envelope)

	case events.CommentCreated:
		return c.handleCommentCreated(ctx, envelope)

	case events.EventUserDeletionRequested:
		var p events.UserDeletionRequestedPayload
		payloadBytes, _ := json.Marshal(envelope.Payload)
		if err := json.Unmarshal(payloadBytes, &p); err != nil {
			return err
		}
		authorID, _ := uuid.Parse(p.UserID)
		if c.timelineStore != nil {
			return c.timelineStore.DeleteTimelineEntriesByAuthor(ctx, authorID)
		}
		return nil

	case events.CrosspostRemoved:
		return c.handleCrosspostRemoved(ctx, envelope)

	case events.UploadDeleted:
		return c.handleUploadDeleted(ctx, envelope)

	case events.HandleChanged:
		return c.handleHandleChanged(ctx, envelope)

	case events.EventPostReposted:
		return c.handlePostReposted(ctx, envelope)

	case events.EventPostRepostUndone:
		return c.handlePostRepostUndone(ctx, envelope)

	case events.EventQAQuestionCreated:
		return c.handleQAQuestionCreated(ctx, envelope)

	case events.EventQAQuestionDeleted, events.EventQAQuestionClosed:
		return c.handleQAQuestionRemoved(ctx, envelope)

	case events.PostContentTypeChanged:
		return c.handlePostContentTypeChanged(ctx, envelope)

	case events.UserFollowed:
		return c.handleUserFollowed(ctx, envelope)

	default:
		return nil
	}
}

// handleUserFollowed backfills the follower's home timeline with the
// followee's recent posts so a freshly-followed account becomes
// visible immediately. Without this, only posts created AFTER the
// follow landed (via PostCreated fan-out on write) ever reached the
// follower, which surfaced as "I followed X but my feed is empty."
//
// We pull the current month's bucket from author_timeline_by_author
// (capped at backfillLimit) and write each row into the follower's
// home_timeline_by_user. Idempotent: AddToHomeTimeline upserts on
// (user_id, bucket, ts, post_id) so re-running on an unfollow + refollow
// cycle is safe.
func (c *Consumer) handleUserFollowed(ctx context.Context, envelope events.EventEnvelope) error {
	if c.timelineStore == nil {
		return nil
	}

	const backfillLimit = 50

	var p events.UserFollowedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return err
	}

	followerID, err := uuid.Parse(p.FollowerID)
	if err != nil {
		return fmt.Errorf("parse follower_id: %w", err)
	}
	followeeID, err := uuid.Parse(p.FolloweeID)
	if err != nil {
		return fmt.Errorf("parse followee_id: %w", err)
	}

	items, err := c.timelineStore.GetAuthorTimeline(ctx, followeeID, backfillLimit)
	if err != nil {
		return fmt.Errorf("read author timeline for backfill: %w", err)
	}
	if len(items) == 0 {
		log.Printf("[feed] UserFollowed: nothing to backfill (follower=%s followee=%s)", followerID, followeeID)
		return nil
	}

	var failures int
	for _, it := range items {
		if err := c.timelineStore.AddToHomeTimeline(ctx, followerID, it.PostID, followeeID, it.CreatedAt, it.ContentType); err != nil {
			failures++
			log.Printf("[feed] UserFollowed backfill: AddToHomeTimeline failed post=%s err=%v", it.PostID, err)
		}
	}
	log.Printf("[feed] UserFollowed backfill: follower=%s followee=%s injected=%d failed=%d", followerID, followeeID, len(items)-failures, failures)
	return nil
}

// handlePostContentTypeChanged rewrites the content_type column on
// every Scylla timeline row that references the post. Fired by
// post-service's MediaTranscodeConsumer after a reclassification
// flips a post (most commonly long_video → flick once transcode
// reveals the real duration + dimensions).
//
// Without this, /v1/feed/reels and /v1/feed/videos keep returning
// stale results — the timeline rows carry their own content_type
// copy that gets written at fan-out time and never updated again.
func (c *Consumer) handlePostContentTypeChanged(ctx context.Context, envelope events.EventEnvelope) error {
	if c.timelineStore == nil {
		return nil
	}
	var p events.PostContentTypeChangedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return err
	}
	postID, err := uuid.Parse(p.PostID)
	if err != nil {
		return fmt.Errorf("PostContentTypeChanged: bad post_id %q: %w", p.PostID, err)
	}
	rows, err := c.timelineStore.UpdatePostContentType(ctx, postID, p.NewType)
	if err != nil {
		return fmt.Errorf("PostContentTypeChanged: update timeline rows: %w", err)
	}
	log.Printf("PostContentTypeChanged: post=%s %s → %s, rewrote %d timeline rows",
		p.PostID, p.OldType, p.NewType, rows)
	return nil
}

// handleQAQuestionCreated fans out a new Q&A question into followers' home
// timelines using content_type = "qa_question". The producer payload carries
// the question_id, author_id, and title — that's enough for the timeline
// write; deeper hydration (community, tags) happens at read time.
func (c *Consumer) handleQAQuestionCreated(ctx context.Context, envelope events.EventEnvelope) error {
	var event struct {
		QuestionID string    `json:"question_id"`
		AuthorID   string    `json:"author_id"`
		Title      string    `json:"title"`
		CreatedAt  time.Time `json:"created_at"`
	}
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}
	questionID, err := uuid.Parse(event.QuestionID)
	if err != nil {
		return nil
	}
	authorID, err := uuid.Parse(event.AuthorID)
	if err != nil {
		return nil
	}
	log.Printf("Processing QAQuestionCreated: %s by %s", event.QuestionID, event.AuthorID)
	return c.service.FanoutQuestion(ctx, questionID, authorID, event.CreatedAt)
}

// handleQAQuestionRemoved hides a question from the feed by flipping the
// shared post:deleted Redis key the hydrator already consults. Both
// deletion and close fire this branch (closed questions should drop out
// of home feeds).
func (c *Consumer) handleQAQuestionRemoved(ctx context.Context, envelope events.EventEnvelope) error {
	var event struct {
		QuestionID string `json:"question_id"`
	}
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}
	questionID, err := uuid.Parse(event.QuestionID)
	if err != nil {
		return nil
	}
	log.Printf("Processing QAQuestion removed: %s", event.QuestionID)
	return c.service.MarkQuestionDeleted(ctx, questionID)
}

func (c *Consumer) handlePostCreated(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostCreatedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}

	postID, err := uuid.Parse(event.PostID)
	if err != nil {
		return err
	}
	authorID, err := uuid.Parse(event.AuthorID)
	if err != nil {
		return err
	}

	// Normalize: empty content_type from old producers defaults to "post"
	contentType := event.ContentType
	if contentType == "" {
		contentType = "post"
	}

	fmt.Printf("Processing PostCreated: %s by %s type=%s\n", event.PostID, event.AuthorID, contentType)
	return c.service.FanoutPost(ctx, postID, authorID, event.CreatedAt, contentType)
}

func (c *Consumer) handlePostReacted(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostReactedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}

	postID := event.PostID
	reactorID := event.ReactorID

	// Increment like counter for velocity tracking
	counterKey := fmt.Sprintf("post:counters:%s:likes", postID)
	if err := c.rdb.Incr(ctx, counterKey).Err(); err != nil {
		log.Printf("Failed to increment like counter for %s: %v", postID, err)
	}
	c.rdb.Expire(ctx, counterKey, 7*24*time.Hour) // 7 days

	// Add reactor to likers set (for already-interacted check)
	likersKey := fmt.Sprintf("post:likers:%s", postID)
	if err := c.rdb.SAdd(ctx, likersKey, reactorID).Err(); err != nil {
		log.Printf("Failed to add reactor to likers set for %s: %v", postID, err)
	}
	c.rdb.Expire(ctx, likersKey, 7*24*time.Hour) // 7 days

	// Write to ScyllaDB as durable interaction record
	if c.timelineStore != nil {
		pid, _ := uuid.Parse(postID)
		uid, _ := uuid.Parse(reactorID)
		if err := c.timelineStore.RecordInteraction(ctx, uid, pid); err != nil {
			log.Printf("Failed to record interaction in ScyllaDB for %s: %v", postID, err)
		}
	}

	log.Printf("Processing PostReacted: post=%s reactor=%s type=%s", postID, reactorID, event.ReactType)
	return nil
}

func (c *Consumer) handleCommentCreated(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.CommentCreatedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}

	postID := event.PostID
	authorID := event.AuthorID

	// Increment comment counter
	counterKey := fmt.Sprintf("post:counters:%s:comments", postID)
	if err := c.rdb.Incr(ctx, counterKey).Err(); err != nil {
		log.Printf("Failed to increment comment counter for %s: %v", postID, err)
	}
	c.rdb.Expire(ctx, counterKey, 7*24*time.Hour) // 7 days

	// Add commenter to interaction set
	likersKey := fmt.Sprintf("post:likers:%s", postID)
	if err := c.rdb.SAdd(ctx, likersKey, authorID).Err(); err != nil {
		log.Printf("Failed to add commenter to interaction set for %s: %v", postID, err)
	}
	c.rdb.Expire(ctx, likersKey, 7*24*time.Hour) // 7 days

	// Write to ScyllaDB as durable interaction record
	if c.timelineStore != nil {
		pid, _ := uuid.Parse(postID)
		uid, _ := uuid.Parse(authorID)
		if err := c.timelineStore.RecordInteraction(ctx, uid, pid); err != nil {
			log.Printf("Failed to record interaction in ScyllaDB for %s: %v", postID, err)
		}
	}

	log.Printf("Processing CommentCreated: post=%s commenter=%s", postID, authorID)
	return nil
}

func (c *Consumer) handleCrosspostRemoved(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.CrosspostRemovedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}

	// The target embed post has been soft-deleted in Postgres.
	// Remove it from home timelines via Redis cache invalidation so
	// hydration will skip it on next feed fetch.
	targetKey := fmt.Sprintf("post:deleted:%s", event.TargetPostID)
	if err := c.rdb.Set(ctx, targetKey, "1", 24*time.Hour).Err(); err != nil {
		log.Printf("Failed to mark crosspost target deleted in Redis: %v", err)
	}

	log.Printf("Processing CrosspostRemoved: crosspost=%s target=%s", event.CrosspostID, event.TargetPostID)
	return nil
}

func (c *Consumer) handleUploadDeleted(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.UploadDeletedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}

	postID, err := uuid.Parse(event.PostID)
	if err != nil {
		return err
	}

	// Mark the post as deleted in Redis so feed hydration skips it
	deletedKey := fmt.Sprintf("post:deleted:%s", event.PostID)
	if err := c.rdb.Set(ctx, deletedKey, "1", 24*time.Hour).Err(); err != nil {
		log.Printf("Failed to mark upload deleted in Redis: %v", err)
	}

	// Clean up interaction counters
	c.rdb.Del(ctx, fmt.Sprintf("post:counters:%s:likes", event.PostID))
	c.rdb.Del(ctx, fmt.Sprintf("post:counters:%s:comments", event.PostID))
	c.rdb.Del(ctx, fmt.Sprintf("post:likers:%s", event.PostID))

	// If author provided, delete from author timeline
	if event.AuthorID != "" {
		authorID, parseErr := uuid.Parse(event.AuthorID)
		if parseErr == nil && c.timelineStore != nil {
			// Delete the specific post entry from author timeline (current month)
			now := time.Now().UTC()
			b := now.Year()*100 + int(now.Month())
			if delErr := c.timelineStore.DeleteAuthorTimelineEntry(ctx, authorID, postID, b); delErr != nil {
				log.Printf("Failed to delete author timeline entry for %s: %v", event.PostID, delErr)
			}
		}
	}

	log.Printf("Processing UploadDeleted: post=%s author=%s type=%s", event.PostID, event.AuthorID, event.ContentType)
	return nil
}

func (c *Consumer) handleHandleChanged(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.HandleChangedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}

	// Invalidate any cached profile data for this user so feed hydration
	// fetches the updated username on next request.
	cacheKey := fmt.Sprintf("feed:profile:%s", event.UserID)
	c.rdb.Del(ctx, cacheKey)

	// Also invalidate old username lookup cache
	oldKey := fmt.Sprintf("feed:handle:%s", event.OldUsername)
	c.rdb.Del(ctx, oldKey)

	log.Printf("Processing HandleChanged: user=%s old=@%s new=@%s", event.UserID, event.OldUsername, event.NewUsername)
	return nil
}

func (c *Consumer) handlePostReposted(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostRepostedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}

	repostID, err := uuid.Parse(event.RepostID)
	if err != nil {
		return err
	}
	originalPostID, err := uuid.Parse(event.OriginalPostID)
	if err != nil {
		return err
	}
	reposterID, err := uuid.Parse(event.ReposterUserID)
	if err != nil {
		return err
	}

	log.Printf("Processing PostReposted: repost=%s original=%s reposter=%s type=%s",
		event.RepostID, event.OriginalPostID, event.ReposterUserID, event.RepostType)

	return c.service.FanoutRepost(ctx, repostID, originalPostID, reposterID, event.CreatedAt, event.Visibility)
}

func (c *Consumer) handlePostRepostUndone(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostRepostUndonePayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}

	repostID, err := uuid.Parse(event.RepostID)
	if err != nil {
		return err
	}
	originalPostID, err := uuid.Parse(event.OriginalPostID)
	if err != nil {
		return err
	}

	log.Printf("Processing PostRepostUndone: repost=%s original=%s reposter=%s",
		event.RepostID, event.OriginalPostID, event.ReposterUserID)

	return c.service.UndoRepostFanout(ctx, repostID, originalPostID)
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
