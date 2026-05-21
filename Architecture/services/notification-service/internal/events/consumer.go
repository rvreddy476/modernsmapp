package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"sync"
	"time"

	"github.com/atpost/notification-service/internal/graph"
	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const likeAggWindow = 5 * time.Second

// friendRequestGraceWindow delays the friend-request push so the
// asynchronous trust-safety verdict (which lands a few hundred ms after
// the ConnectionRequested event) has time to mark abusive requests
// filtered. 5s comfortably exceeds trust-safety's scoring latency. P1.4b.
const friendRequestGraceWindow = 5 * time.Second

// likeAggEntry tracks accumulated likes for a single post+author pair.
type likeAggEntry struct {
	postAuthorID uuid.UUID
	postID       uuid.UUID
	deepLink     string
	actors       []uuid.UUID
	timer        *time.Timer
	createdAt    time.Time
}

// friendReqEntry holds a pending friend-request notification awaiting the
// trust-safety grace window. P1.4b.
type friendReqEntry struct {
	senderID   uuid.UUID
	receiverID uuid.UUID
	deepLink   string
	createdAt  time.Time
	timer      *time.Timer
}

type Consumer struct {
	reader  *kafka.Reader
	service *service.Service
	graph   *graph.Client // optional — fan-out for upload notifications

	// Like aggregation: key = "postID:postAuthorID"
	likeAgg   map[string]*likeAggEntry
	likeAggMu sync.Mutex

	// Friend-request grace timers: key = "senderID:receiverID". P1.4b.
	friendReq   map[string]*friendReqEntry
	friendReqMu sync.Mutex
}

// WithGraph attaches a graph-service client. Required for fanning out
// follower notifications on PostCreated; the consumer otherwise no-ops
// for that event.
func (c *Consumer) WithGraph(g *graph.Client) *Consumer {
	c.graph = g
	return c
}

func NewConsumer(brokers []string, groupID string, topic string, svc *service.Service) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, topic, svc, nil)
}

func NewConsumerWithDialer(brokers []string, groupID string, topic string, svc *service.Service, dialer *kafka.Dialer) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
		Dialer:   dialer,
	})
	return &Consumer{
		reader:    reader,
		service:   svc,
		likeAgg:   make(map[string]*likeAggEntry),
		friendReq: make(map[string]*friendReqEntry),
	}
}

// Start runs the consumer with a bounded worker pool. Audit CR1:
// previously processMessage ran inline on the read loop, so one slow
// handler (Scylla timeout, graph round-trip, follower fan-out) stalled
// the entire topic. Now a buffered channel decouples reading from
// dispatching; numWorkers handlers run in parallel.
//
// Ordering trade-off: events are processed concurrently, so two
// updates against the same recipient can race. The aggregation layer
// (CR4) is now atomic in Redis so per-recipient state stays consistent;
// rare reorderings between distinct event types are acceptable —
// notifications are individually idempotent on the dedup table.
func (c *Consumer) Start(ctx context.Context) {
	const numWorkers = 16
	const bufferSize = 1024

	jobs := make(chan kafka.Message, bufferSize)
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(id int) {
			defer wg.Done()
			for m := range jobs {
				if err := c.processMessage(ctx, m); err != nil {
					log.Printf("worker %d failed to process message: %v\n", id, err)
				}
			}
		}(i)
	}

	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("notification consumer shutting down")
			} else {
				log.Printf("Consumer error: %v\n", err)
			}
			break
		}
		select {
		case jobs <- m:
		case <-ctx.Done():
			break
		}
	}
	close(jobs)
	wg.Wait()
}

func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}

	switch envelope.EventType {
	case events.UserFollowed:
		var e events.UserFollowedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		followerID, _ := uuid.Parse(e.FollowerID)
		followeeID, _ := uuid.Parse(e.FolloweeID) // We notify the followee

		deepLink := fmt.Sprintf("/u/%s", e.FollowerID)
		return c.service.CreateNotification(ctx, followeeID, followerID, "follow", "user", followerID, deepLink, e.CreatedAt)

	case events.PostCreated:
		// Fan out a notification to every follower of the author whenever
		// they upload a video or flick. Skip text/poll posts — those flow
		// through the regular feed and shouldn't push-spam followers.
		var e events.PostCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}
		// Audit CR2: dispatch fanout asynchronously so the consumer
		// goroutine isn't pinned on a 5k-follower upload for tens of
		// seconds.
		c.FanOutCreatorUploadAsync(e)
		return nil

	case events.PostReacted:
		var e events.PostReactedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		reactorID, _ := uuid.Parse(e.ReactorID)
		postAuthorID, _ := uuid.Parse(e.PostAuthorID)
		postID, _ := uuid.Parse(e.PostID)

		// Don't notify if reacting to own post
		if reactorID == postAuthorID {
			return nil
		}

		// Aggregate likes in a 5-second window to avoid notification spam on viral posts
		c.aggregateLike(postID, postAuthorID, reactorID, e.PostID, e.CreatedAt)
		return nil

	case events.CommentReacted:
		var e events.CommentReactedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		reactorID, _ := uuid.Parse(e.ReactorID)
		commentAuthorID, _ := uuid.Parse(e.CommentAuthorID)
		commentID, _ := uuid.Parse(e.CommentID)

		// Don't notify if reacting to own comment
		if reactorID == commentAuthorID {
			return nil
		}

		deepLink := fmt.Sprintf("/post/%s?focusComment=%s", e.PostID, e.CommentID)
		return c.service.CreateNotification(ctx, commentAuthorID, reactorID, "comment_reaction", "comment", commentID, deepLink, e.CreatedAt)

	case events.CommentCreated:
		var e events.CommentCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		commentAuthorID, _ := uuid.Parse(e.AuthorID)
		postAuthorID, _ := uuid.Parse(e.PostAuthorID)
		postID, _ := uuid.Parse(e.PostID)

		// Don't notify if commenting on own post
		if commentAuthorID == postAuthorID {
			return nil
		}

		deepLink := fmt.Sprintf("/post/%s?focusComment=%s", e.PostID, e.CommentID)
		return c.service.CreateNotification(ctx, postAuthorID, commentAuthorID, "comment", "post", postID, deepLink, e.CreatedAt)

	case events.ConnectionRequested:
		var e events.ConnectionRequestedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		senderID, _ := uuid.Parse(e.SenderID)
		receiverID, _ := uuid.Parse(e.ReceiverID)

		// P1.4b: do NOT push immediately. trust-safety-service scores the
		// request asynchronously and may mark it filtered a few hundred ms
		// after this event. Delay behind a grace window, then re-check the
		// verdict before dispatching.
		deepLink := fmt.Sprintf("/u/%s", e.SenderID)
		c.scheduleFriendRequest(senderID, receiverID, deepLink, e.CreatedAt)
		return nil

	case events.ConnectionAccepted:
		var e events.ConnectionAcceptedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		senderID, _ := uuid.Parse(e.SenderID)
		receiverID, _ := uuid.Parse(e.ReceiverID)

		deepLink := fmt.Sprintf("/u/%s", e.ReceiverID)
		return c.service.CreateNotification(ctx, senderID, receiverID, "friend_accepted", "user", receiverID, deepLink, e.AcceptedAt)

	case events.StoryCreated:
		var e events.StoryCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}
		// Story notifications are handled client-side via the story feed; no push notification needed
		return nil

	case events.UserEndorsed:
		var e events.UserEndorsedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		fromUserID, _ := uuid.Parse(e.FromUserID)
		toUserID, _ := uuid.Parse(e.ToUserID)

		deepLink := fmt.Sprintf("/u/%s", e.FromUserID)
		return c.service.CreateNotification(ctx, toUserID, fromUserID, "endorsement", "user", fromUserID, deepLink, e.CreatedAt)

	case events.BusinessReviewCreated:
		var e events.BusinessReviewCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		pageOwnerID, _ := uuid.Parse(e.PageOwner)
		reviewerID, _ := uuid.Parse(e.ReviewerID)
		pageID, _ := uuid.Parse(e.PageID)

		if pageOwnerID == reviewerID {
			return nil
		}

		deepLink := fmt.Sprintf("/page/%s", e.PageID)
		return c.service.CreateNotification(ctx, pageOwnerID, reviewerID, "business_review", "business_page", pageID, deepLink, e.CreatedAt)

	case events.SubscriptionCreated:
		var e events.SubscriptionCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		subscriberID, _ := uuid.Parse(e.SubscriberID)
		creatorID, _ := uuid.Parse(e.CreatorID)

		deepLink := fmt.Sprintf("/u/%s", e.SubscriberID)
		return c.service.CreateNotification(ctx, creatorID, subscriberID, "new_subscriber", "user", subscriberID, deepLink, e.CreatedAt)

	case events.GroupMemberJoined:
		var e events.GroupMemberJoinedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}
		// Group join notifications are handled by the group service itself
		return nil

	case events.EventUserDeletionRequested:
		var e events.UserDeletionRequestedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}
		userID, _ := uuid.Parse(e.UserID)
		// Delete all notifications for this user
		if err := c.service.DeleteNotificationsForUser(ctx, userID); err != nil {
			log.Printf("notification: failed to delete notifications for user %s: %v\n", e.UserID, err)
		}
		// Deactivate all device tokens
		return c.service.DeactivateDeviceTokens(ctx, userID)

	case events.EventUserMentioned:
		var e events.UserMentionedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		mentionedID, _ := uuid.Parse(e.MentionedUserID)
		authorID, _ := uuid.Parse(e.AuthorID)
		postID, _ := uuid.Parse(e.PostID)
		deepLink := fmt.Sprintf("/post/%s", e.PostID)
		return c.service.CreateNotification(ctx, mentionedID, authorID, "mention", "post", postID, deepLink, e.OccurredAt)

	case events.EventPostReposted:
		var e events.PostRepostedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		reposterID, _ := uuid.Parse(e.ReposterUserID)
		originalAuthorID, _ := uuid.Parse(e.OriginalAuthorID)
		originalPostID, _ := uuid.Parse(e.OriginalPostID)

		// Don't notify if reposting own post
		if reposterID == originalAuthorID {
			return nil
		}

		// Notification type: "post_reposted"
		deepLink := fmt.Sprintf("/post/%s", e.OriginalPostID)
		return c.service.CreateNotification(ctx, originalAuthorID, reposterID, "post_reposted", "post", originalPostID, deepLink, e.CreatedAt)

	case "commerce.order.created",
		"commerce.order.paid",
		"commerce.order.shipped",
		"commerce.order.delivered",
		"commerce.invoice.issued",
		"commerce.seller.new_order":
		return c.handleCommerceEvent(ctx, envelope.EventType, envelope.Payload)

	default:
		// Q&A events are routed through a dedicated handler so the producer
		// payloads can stay narrow (we look up author_id via internal HTTP
		// to qa-service when the payload doesn't carry it).
		if handled, err := c.handleQAEvent(ctx, envelope); handled {
			return err
		}
		// Dating events (Sprint 3): spark.created, spark.matched,
		// match.first_message, match.expired. Other dating.* events are
		// claimed and silently ignored.
		if handled, err := c.handleDatingEvent(ctx, envelope); handled {
			return err
		}
		// Rider (Mopedu) events (Sprint 3): safety.sos, complaint.raised,
		// partner.approved, subscription.expiring. Other rider.* events are
		// claimed and silently ignored.
		if handled, err := c.handleRiderEvent(ctx, envelope); handled {
			return err
		}
		// Food (FiGo) events: order placed / payment / cancelled / refunded
		// fan out as notifications + FCM to the customer + restaurant + admin.
		if handled, err := c.handleFoodEvent(ctx, envelope); handled {
			return err
		}
		return nil
	}
}

// aggregateLike batches like notifications in a 5-second window.
// On first like for a post+author pair, starts a timer. Subsequent likes within
// the window accumulate actors. When the timer fires, a single aggregated
// notification is created with the most recent reactor as the actor.
func (c *Consumer) aggregateLike(postID, postAuthorID, reactorID uuid.UUID, postIDStr string, createdAt time.Time) {
	key := fmt.Sprintf("%s:%s", postID.String(), postAuthorID.String())

	c.likeAggMu.Lock()
	defer c.likeAggMu.Unlock()

	entry, exists := c.likeAgg[key]
	if exists {
		// Append actor to existing window
		entry.actors = append(entry.actors, reactorID)
		return
	}

	// First like in this window — create entry and start timer
	entry = &likeAggEntry{
		postAuthorID: postAuthorID,
		postID:       postID,
		deepLink:     fmt.Sprintf("/post/%s", postIDStr),
		actors:       []uuid.UUID{reactorID},
		createdAt:    createdAt,
	}
	entry.timer = time.AfterFunc(likeAggWindow, func() {
		c.flushLikeAgg(key)
	})
	c.likeAgg[key] = entry
}

// flushLikeAgg fires when the aggregation window expires, creating a single notification.
func (c *Consumer) flushLikeAgg(key string) {
	c.likeAggMu.Lock()
	entry, exists := c.likeAgg[key]
	if !exists {
		c.likeAggMu.Unlock()
		return
	}
	delete(c.likeAgg, key)
	c.likeAggMu.Unlock()

	// Use the most recent reactor as the notification actor
	lastActor := entry.actors[len(entry.actors)-1]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.service.CreateNotification(
		ctx,
		entry.postAuthorID,
		lastActor,
		"reaction",
		"post",
		entry.postID,
		entry.deepLink,
		entry.createdAt,
	); err != nil {
		log.Printf("Failed to flush aggregated like notification for %s: %v\n", key, err)
	}
}

// scheduleFriendRequest registers a friend-request notification to be
// dispatched after friendRequestGraceWindow. P1.4b: the push is delayed so
// trust-safety-service has time to mark abusive requests filtered. Mirrors
// the aggregateLike timer pattern (mutex-guarded map + *time.Timer).
//
// A duplicate ConnectionRequested for the same sender+receiver pair within
// the window is ignored — the existing timer already covers it.
func (c *Consumer) scheduleFriendRequest(senderID, receiverID uuid.UUID, deepLink string, createdAt time.Time) {
	key := fmt.Sprintf("%s:%s", senderID.String(), receiverID.String())

	c.friendReqMu.Lock()
	defer c.friendReqMu.Unlock()

	if _, exists := c.friendReq[key]; exists {
		return
	}

	entry := &friendReqEntry{
		senderID:   senderID,
		receiverID: receiverID,
		deepLink:   deepLink,
		createdAt:  createdAt,
	}
	entry.timer = time.AfterFunc(friendRequestGraceWindow, func() {
		c.flushFriendRequest(key)
	})
	c.friendReq[key] = entry
}

// flushFriendRequest fires when the grace window expires. It asks
// graph-service for the recipient's auto-filtered (hidden) requests; if the
// event's sender is in that set the push is suppressed, otherwise the
// friend-request notification is dispatched as the original handler did.
//
// Fail-open: on ANY graph error (request fails, non-200, parse error) the
// push is dispatched — an internal blip must never drop a legitimate
// notification. Recovers from panics so a misbehaving timer can't crash the
// consumer.
func (c *Consumer) flushFriendRequest(key string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("friend-request grace timer panicked", "key", key, "panic", r)
		}
	}()

	c.friendReqMu.Lock()
	entry, exists := c.friendReq[key]
	if !exists {
		c.friendReqMu.Unlock()
		return
	}
	delete(c.friendReq, key)
	c.friendReqMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check the trust-safety verdict via graph-service. Fail-open on error.
	if c.graph != nil {
		filtered, err := c.graph.GetFilteredConnectionRequestSenders(ctx, entry.receiverID)
		if err != nil {
			slog.Warn("friend-request: filtered-requests lookup failed; failing open",
				"sender", entry.senderID, "receiver", entry.receiverID, "error", err)
		} else if _, isFiltered := filtered[entry.senderID]; isFiltered {
			slog.Info("friend-request push suppressed: request was auto-filtered",
				"sender", entry.senderID, "receiver", entry.receiverID)
			return
		}
	}

	if err := c.service.CreateNotification(
		ctx,
		entry.receiverID,
		entry.senderID,
		"friend_request",
		"user",
		entry.senderID,
		entry.deepLink,
		entry.createdAt,
	); err != nil {
		slog.Error("friend-request: failed to dispatch notification",
			"sender", entry.senderID, "receiver", entry.receiverID, "error", err)
	}
}

func unmarshalPayload(raw json.RawMessage, v interface{}) error {
	b, _ := json.Marshal(raw)
	return json.Unmarshal(b, v)
}

// fanOutCreatorUpload pages through the author's follower list and creates
// one notification row per follower for the new video/flick. Best-effort:
// failures on individual followers are logged and skipped so one bad row
// can't poison the whole batch.
//
// Filters:
//   - Only video/flick content_types push (text/poll posts go via the feed).
//   - Public + followers visibility only — private posts don't notify.
//   - Author themselves is excluded by graph-service (followers != self).
//
// Page size 200, capped at 5,000 followers per upload to bound the cost.
// Audit CR2: this used to run synchronously inside processMessage, so
// a celeb upload pinned the consumer goroutine for tens of seconds
// (5,000 sequential Scylla writes + 25 graph paginations). Now the
// caller dispatches via FanOutCreatorUploadAsync, and within this
// function the per-recipient CreateNotification calls run on a
// per-event sub-pool of 16 workers so even one event's fan-out
// doesn't sit waiting on serial Scylla latency.
func (c *Consumer) fanOutCreatorUpload(ctx context.Context, e events.PostCreatedPayload) error {
	switch e.ContentType {
	case "flick", "long_video", "reel", "video":
		// supported — fall through
	default:
		return nil
	}
	if e.Visibility == "private" || e.Visibility == "unlisted" {
		return nil
	}
	if c.graph == nil {
		// No graph client wired (dev?); silently skip rather than failing.
		return nil
	}

	authorID, err := uuid.Parse(e.AuthorID)
	if err != nil {
		return nil // bad payload, don't retry
	}
	postID, err := uuid.Parse(e.PostID)
	if err != nil {
		return nil
	}

	const pageSize = 200
	const maxFollowers = 5000
	deepLink := fmt.Sprintf("/posttube/watch/%s", e.PostID)
	if e.ContentType == "flick" || e.ContentType == "reel" {
		deepLink = fmt.Sprintf("/reels/%s", e.PostID)
	}

	notifType := "creator_uploaded_video"
	if e.ContentType == "flick" || e.ContentType == "reel" {
		notifType = "creator_uploaded_flick"
	}

	// Per-event sub-pool: parallelize the Scylla writes for one
	// upload so a 5,000-follower fanout finishes in ~5k/16/ratePerWorker
	// instead of serial.
	const fanoutWorkers = 16
	jobs := make(chan uuid.UUID, fanoutWorkers*2)
	var wg sync.WaitGroup
	var deliveredMu sync.Mutex
	delivered := 0
	for w := 0; w < fanoutWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fid := range jobs {
				if err := c.service.CreateNotification(
					ctx, fid, authorID, notifType, "post", postID, deepLink, e.CreatedAt,
				); err != nil {
					slog.Warn("creator upload fan-out: notify failed",
						"follower", fid, "post_id", postID, "error", err)
					continue
				}
				deliveredMu.Lock()
				delivered++
				deliveredMu.Unlock()
			}
		}()
	}

	for offset := 0; offset < maxFollowers; offset += pageSize {
		followers, err := c.graph.GetFollowers(ctx, authorID, pageSize, offset)
		if err != nil {
			slog.Warn("creator upload fan-out: followers fetch failed",
				"author_id", authorID, "offset", offset, "error", err)
			break
		}
		if len(followers) == 0 {
			break
		}
		for _, fid := range followers {
			jobs <- fid
		}
		if len(followers) < pageSize {
			break
		}
	}
	close(jobs)
	wg.Wait()
	slog.Info("creator upload fan-out complete",
		"author_id", authorID, "post_id", postID,
		"content_type", e.ContentType, "delivered", delivered)
	return nil
}

// FanOutCreatorUploadAsync wraps fanOutCreatorUpload in a goroutine so
// the consumer loop never blocks on it. Audit CR2: a single celeb
// upload (5k followers, ~25 graph paginations) previously paused the
// consumer for tens of seconds and starved every other event behind it.
// Now the consumer continues immediately; fanout runs on a fresh
// background context so request-cancel doesn't stop the delivery.
func (c *Consumer) FanOutCreatorUploadAsync(e events.PostCreatedPayload) {
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := c.fanOutCreatorUpload(bg, e); err != nil {
			slog.Warn("async creator upload fan-out failed", "post_id", e.PostID, "error", err)
		}
	}()
}

func (c *Consumer) Close() error {
	// Stop any pending delayed-dispatch timers so they don't fire after the
	// consumer has shut down. Like-aggregation and friend-request grace
	// timers are handled identically.
	c.likeAggMu.Lock()
	for key, entry := range c.likeAgg {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.likeAgg, key)
	}
	c.likeAggMu.Unlock()

	c.friendReqMu.Lock()
	for key, entry := range c.friendReq {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.friendReq, key)
	}
	c.friendReqMu.Unlock()

	return c.reader.Close()
}
