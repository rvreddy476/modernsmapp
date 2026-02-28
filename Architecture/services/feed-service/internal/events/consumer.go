package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/facebook-like/feed-service/internal/service"
	"github.com/facebook-like/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader  *kafka.Reader
	service *service.Service
	rdb     *redis.Client
}

func NewConsumer(brokers []string, groupID string, topic string, svc *service.Service, rdb *redis.Client) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})
	return &Consumer{reader: reader, service: svc, rdb: rdb}
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

	default:
		return nil
	}
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

	fmt.Printf("Processing PostCreated: %s by %s\n", event.PostID, event.AuthorID)
	return c.service.FanoutPost(ctx, postID, authorID, event.CreatedAt)
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
	// Set TTL of 48h on counter (auto-cleanup old posts)
	c.rdb.Expire(ctx, counterKey, 48*time.Hour) // 48 hours

	// Add reactor to likers set (for already-interacted check)
	likersKey := fmt.Sprintf("post:likers:%s", postID)
	if err := c.rdb.SAdd(ctx, likersKey, reactorID).Err(); err != nil {
		log.Printf("Failed to add reactor to likers set for %s: %v", postID, err)
	}
	c.rdb.Expire(ctx, likersKey, 48*time.Hour)

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
	c.rdb.Expire(ctx, counterKey, 48*time.Hour)

	// Add commenter to likers set (treat comments as interactions too)
	likersKey := fmt.Sprintf("post:likers:%s", postID)
	if err := c.rdb.SAdd(ctx, likersKey, authorID).Err(); err != nil {
		log.Printf("Failed to add commenter to interaction set for %s: %v", postID, err)
	}
	c.rdb.Expire(ctx, likersKey, 48*time.Hour)

	log.Printf("Processing CommentCreated: post=%s commenter=%s", postID, authorID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
