package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/facebook-like/notification-service/internal/service"
	"github.com/facebook-like/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const likeAggWindow = 5 * time.Second

// likeAggEntry tracks accumulated likes for a single post+author pair.
type likeAggEntry struct {
	postAuthorID uuid.UUID
	postID       uuid.UUID
	deepLink     string
	actors       []uuid.UUID
	timer        *time.Timer
	createdAt    time.Time
}

type Consumer struct {
	reader  *kafka.Reader
	service *service.Service

	// Like aggregation: key = "postID:postAuthorID"
	likeAgg   map[string]*likeAggEntry
	likeAggMu sync.Mutex
}

func NewConsumer(brokers []string, groupID string, topic string, svc *service.Service) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})
	return &Consumer{
		reader:  reader,
		service: svc,
		likeAgg: make(map[string]*likeAggEntry),
	}
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
	case events.UserFollowed:
		var e events.UserFollowedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		followerID, _ := uuid.Parse(e.FollowerID)
		followeeID, _ := uuid.Parse(e.FolloweeID) // We notify the followee

		deepLink := fmt.Sprintf("/u/%s", e.FollowerID)
		return c.service.CreateNotification(ctx, followeeID, followerID, "follow", "user", followerID, deepLink, e.CreatedAt)

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

	case events.FriendRequestSent:
		var e events.FriendRequestSentPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		senderID, _ := uuid.Parse(e.SenderID)
		receiverID, _ := uuid.Parse(e.ReceiverID)

		deepLink := fmt.Sprintf("/u/%s", e.SenderID)
		return c.service.CreateNotification(ctx, receiverID, senderID, "friend_request", "user", senderID, deepLink, e.CreatedAt)

	case events.FriendRequestAccepted:
		var e events.FriendRequestAcceptedPayload
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

	default:
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

func unmarshalPayload(raw json.RawMessage, v interface{}) error {
	b, _ := json.Marshal(raw)
	return json.Unmarshal(b, v)
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
