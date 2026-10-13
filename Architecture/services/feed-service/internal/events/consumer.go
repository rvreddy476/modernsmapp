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
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// DefaultDLQTopic is where a feed event lands once its retries are spent.
// It already exists in every environment (infra/terraform/modules/msk/
// topics-job/variables.tf) and is shared with the other social.events.v1
// consumers; the x-dlq-consumer-group / x-dlq-original-topic headers the
// shared consumer stamps say whose message it is.
const DefaultDLQTopic = "social.events.v1.dlq"

// Consumer applies social events to the feed's timelines and caches.
//
// Delivery is the shared at-least-once consumer (shared/kafka): the offset
// is committed only after the handler succeeded or the message was durably
// written to the DLQ, a failed handler is retried with backoff first, and a
// processed event leaves a receipt so a redelivery of the same event_id is
// skipped. It replaced a kafka-go ReadMessage loop that committed every
// offset BEFORE the handler ran and only logged what came back — so any
// transient Scylla, graph or Postgres error lost the fan-out, delete or
// unfollow purge for good — and that exited on its first read error, after
// which the pod stayed Ready and never consumed again.
//
// The shared reader uses MinBytes 1, so a quiet topic is still delivered at
// once (the ten-second fan-out lag came from MinBytes 10 KB, not from here).
//
// Every handler must tolerate running twice for one event.
type Consumer struct {
	inner         *sharedkafka.Consumer
	service       *service.Service
	rdb           *redis.Client
	timelineStore *scylla.TimelineStore
}

// NewConsumer builds the consumer for one topic. An empty dlqTopic disables
// the DLQ: a message whose retries are spent then blocks its partition until
// it succeeds (shared/kafka retryDurable) instead of being skipped.
func NewConsumer(brokers []string, groupID, topic, dlqTopic string, svc *service.Service, rdb *redis.Client, ts *scylla.TimelineStore, m *metrics.KafkaConsumerMetrics) *Consumer {
	c := &Consumer{service: svc, rdb: rdb, timelineStore: ts}
	c.inner = sharedkafka.NewConsumer(sharedkafka.ConsumerConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		DLQTopic: dlqTopic,
	}, rdb, m, c.handle)
	return c
}

// Start blocks until ctx is cancelled. A fetch error is logged and retried
// by the shared consumer; it no longer ends consumption.
func (c *Consumer) Start(ctx context.Context) {
	c.inner.Start(ctx)
}

// handle is the shared consumer's handler; the envelope is already decoded.
func (c *Consumer) handle(ctx context.Context, envelope *events.EventEnvelope) error {
	return c.dispatch(ctx, *envelope)
}

// processMessage decodes a raw message and dispatches it. Production goes
// through handle; tests feed raw messages here.
func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}
	return c.dispatch(ctx, envelope)
}

func (c *Consumer) dispatch(ctx context.Context, envelope events.EventEnvelope) error {
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

	// Soft delete / restore (2026-09-04) — see post_lifecycle.go.
	case events.PostDeleted:
		return c.handlePostDeleted(ctx, envelope)

	case events.PostRestored:
		return c.handlePostRestored(ctx, envelope)

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

	case events.PostDistributionUpdated:
		return c.handlePostDistributionUpdated(ctx, envelope)

	case events.UserFollowed:
		return c.handleUserFollowed(ctx, envelope)

	case events.UserUnfollowed:
		return c.handleUserUnfollowed(ctx, envelope)

	// Tube channel subscriptions: the viewer's cached subscribed-owner
	// set is stale the moment either lands. See subscriptions.go.
	case events.TubeChannelSubscribed, events.TubeChannelUnsubscribed:
		return c.handleChannelSubscriptionChanged(ctx, envelope)

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
// We pull up to backfillLimit rows from author_timeline_by_author
// across the last bucketLookback months and write each into
// home_timeline_by_user.
//
// Idempotent: AddToHomeTimeline keys each row by scylla.TimelineTS, a pure
// function of (post, created_at), so a follow → unfollow → refollow cycle
// or a redelivery rewrites the same rows. Until that key existed every
// backfill added a fresh copy of the backlog (seven per post on the SSO
// test account on 2026-09-09).
func (c *Consumer) handleUserFollowed(ctx context.Context, envelope events.EventEnvelope) error {
	if c.timelineStore == nil {
		return nil
	}

	const backfillLimit = 50
	const bucketLookback = 3

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

	// Audit HF6: celebs use the pull model (FanoutPost short-circuits
	// for them — feed.go:467), so a backfill into the new follower's
	// timeline would diverge from the read path: nothing else lands
	// there until the next celeb post is fetched on-read. Skip the
	// inline backfill and let the read-time hydrator surface celeb
	// content. Also avoids a thundering-herd of backfills when many
	// follows arrive for a single celeb in a short window.
	if c.service != nil {
		if isCeleb, _ := c.service.IsCelebAuthor(ctx, followeeID); isCeleb {
			log.Printf("[feed] UserFollowed backfill skipped — followee=%s is celeb (pull model)", followeeID)
			return nil
		}
	}

	items, err := c.timelineStore.GetAuthorTimelineMultiBucket(ctx, followeeID, backfillLimit, bucketLookback)
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

// handleUserUnfollowed purges every row from the follower's
// home_timeline_by_user where author_id == followee, so an unfollow
// reflects immediately in the feed (both the fan-out-on-write rows and
// the backfilled rows from the matching UserFollowed event go away).
// Scoped to the last 3 buckets — older entries age out naturally.
//
// The purge is gated on service.FanoutClaimAfterUnfollow: FanoutPost
// targets the author's followers UNION their connections (and, for
// "trusted" posts, their close friends), so an unfollow alone does not
// establish that these rows should go. A relationship that cannot be
// established leaves everything in place — see unfollow_purge.go, FAILING
// CLOSED. TestUnfollowPurgeIsGuardedByTheClaimCheck pins the gate.
//
// An error returned from here is retried by the shared consumer and then
// parked on the DLQ, so a relationship that could not be established is
// revisited rather than abandoned. The purge itself is idempotent.
func (c *Consumer) handleUserUnfollowed(ctx context.Context, envelope events.EventEnvelope) error {
	if c.timelineStore == nil {
		return nil
	}

	const bucketLookback = 3

	var p events.UserUnfollowedPayload
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

	if c.service == nil {
		return fmt.Errorf("purge home timeline on unfollow: no service to establish the " +
			"follower's remaining relationship; nothing purged")
	}
	retained, why, err := c.service.FanoutClaimAfterUnfollow(ctx, followerID, followeeID)
	if err != nil {
		return fmt.Errorf("purge home timeline on unfollow: relationship for follower=%s "+
			"followee=%s not established, nothing purged: %w", followerID, followeeID, err)
	}
	if retained {
		log.Printf("[feed] UserUnfollowed: purge skipped — follower=%s %s (followee=%s); "+
			"the fanout still targets them", followerID, why, followeeID)
		return nil
	}

	deleted, err := c.timelineStore.DeleteHomeTimelineEntriesByAuthorForUser(ctx, followerID, followeeID, bucketLookback)
	if err != nil {
		return fmt.Errorf("purge home timeline on unfollow: %w", err)
	}
	log.Printf("[feed] UserUnfollowed: purged %d row(s) follower=%s entries from followee=%s", deleted, followerID, followeeID)
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

	// P0-1: record the post's main-feed eligibility from the creation
	// policy. Fan-out still happens for every post — PostTube surfaces
	// (/feed/videos, /feed/watch) read the same home timeline, so a
	// PostTube-only video must still reach it; the social-home read path
	// filters against feed_distribution.
	//
	// fixes-v2 / Codex P1-2: write a row for EVERY post, not only
	// policy-bearing ones. The row is the authoritative row-level
	// provenance marker — "eligible because the producer said so" is now
	// distinguishable from "no policy row exists". Producers that send no
	// policy get main_feed=true, which preserves prior behavior while
	// still recording that we saw the post.
	mainFeed := true
	if event.MainFeed != nil {
		mainFeed = *event.MainFeed
	}
	rev := event.DistributionRev
	if rev == 0 {
		rev = 1
	}
	if err := c.service.RecordDistribution(ctx, postID, mainFeed, rev); err != nil {
		// Fail the message so the consumer retries (then DLQs) it: losing
		// this row would leak a PostTube-only video into social home.
		return fmt.Errorf("record distribution for %s: %w", event.PostID, err)
	}

	// The author's canonical Tube channel, when post-service stamped one.
	// It keys the subscriber leg of the fan-out (long videos only); a
	// missing or malformed id skips that leg rather than failing the
	// message, because the followers' and circle's rows above must still
	// land even when the channel side of the event is broken.
	channelID := uuid.Nil
	if event.ChannelID != "" {
		parsed, err := uuid.Parse(event.ChannelID)
		if err != nil {
			log.Printf("PostCreated %s: ignoring malformed channel_id %q: %v", event.PostID, event.ChannelID, err)
		} else {
			channelID = parsed
		}
	}

	fmt.Printf("Processing PostCreated: %s by %s type=%s\n", event.PostID, event.AuthorID, contentType)
	return c.service.FanoutPost(ctx, postID, authorID, event.CreatedAt, contentType, event.Visibility, channelID)
}

// handleChannelSubscriptionChanged drops the subscriber's cached
// subscribed-owner set on tube.channel.subscribed / unsubscribed, so the
// next Subscriptions page (and the ranker's boost one page after it)
// rebuilds the set from post-service rather than serving up to six hours
// of the old answer. A DEL rather than a targeted SADD / SREM: the event
// carries one owner but the set was built from a paged read, and a set
// edited in place cannot tell a partial rebuild from a complete one. The
// "looked" marker goes with it, or an emptied set would read as "looked
// and found none" and never be refetched.
func (c *Consumer) handleChannelSubscriptionChanged(ctx context.Context, envelope events.EventEnvelope) error {
	if c.service == nil {
		return nil
	}
	var event events.ChannelSubscriptionPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}
	subscriberID, err := uuid.Parse(event.SubscriberID)
	if err != nil {
		return fmt.Errorf("%s: bad subscriber_id %q: %w", envelope.EventType, event.SubscriberID, err)
	}
	return c.service.InvalidateSubscribedOwners(ctx, subscriberID)
}

// handlePostDistributionUpdated applies a post-creation policy change.
// The store upsert is rev-guarded, so stale/replayed events are no-ops
// (Codex P0-1: replay creates no duplicates; monotonic rev protection).
func (c *Consumer) handlePostDistributionUpdated(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostDistributionUpdatedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}
	postID, err := uuid.Parse(event.PostID)
	if err != nil {
		return err
	}
	if err := c.service.RecordDistribution(ctx, postID, event.MainFeed, event.DistributionRev); err != nil {
		return fmt.Errorf("record distribution update for %s: %w", event.PostID, err)
	}
	log.Printf("Processing PostDistributionUpdated: post=%s main_feed=%v rev=%d",
		event.PostID, event.MainFeed, event.DistributionRev)
	return nil
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

	// Remove the post from every timeline that holds it, as PostDeleted
	// does. This used to delete only the author's own row, and only from
	// the CURRENT month's bucket — a post uploaded last month kept its
	// author row, and every follower kept theirs (masked only by the
	// tombstone and post-service's batch gate, but still counted by the
	// new-posts pill and still taking slots in each page's window).
	removed, err := c.removePostFromTimelines(ctx, postID, event.AuthorID)
	if err != nil {
		return fmt.Errorf("UploadDeleted: %w", err)
	}

	log.Printf("Processing UploadDeleted: post=%s author=%s type=%s timeline_rows_removed=%d",
		event.PostID, event.AuthorID, event.ContentType, removed)
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
	if c.inner == nil {
		return nil
	}
	return c.inner.Close()
}
