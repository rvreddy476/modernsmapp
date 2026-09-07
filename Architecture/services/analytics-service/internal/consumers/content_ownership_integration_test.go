//go:build integration

package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/postclassify"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

func TestLivePostCreatedProjectsCanonicalOwnership(t *testing.T) {
	broker := os.Getenv("KAFKA_BROKERS")
	if broker == "" {
		t.Skip("KAFKA_BROKERS is required")
	}
	ctx := context.Background()
	// Own database, own truncates: see internal/testsupport.
	pool := testsupport.Pool(t, "consumers")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.ingest_receipts, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}

	content, creator := uuid.New(), uuid.New()
	creatorStr := creator.String()
	payload := events.PostCreatedPayload{
		PostID: content.String(), AuthorID: creator.String(),
		ContentType: postclassify.LongVideo, CreatedAt: time.Now().UTC(),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope := events.NewEnvelope(ctx, events.PostCreated, &payload.AuthorID, payloadJSON)
	value, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	topic := fmt.Sprintf("m6-ownership-%d", time.Now().UnixNano())
	// Create the topic before anyone reads it. Left to auto-creation, the
	// reader spends its first seconds refreshing metadata for a topic that
	// does not exist yet, on top of the consumer group's first rebalance —
	// which is why this test used to sit at 11-17 seconds against a
	// 15-second budget and fail roughly one run in three, on its own, with
	// nothing else competing for the broker.
	client := &kafka.Client{Addr: kafka.TCP(broker), Timeout: 10 * time.Second}
	if _, err := client.CreateTopics(ctx, &kafka.CreateTopicsRequest{
		Topics: []kafka.TopicConfig{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	writer := &kafka.Writer{Addr: kafka.TCP(broker), Topic: topic, AllowAutoTopicCreation: true}
	if err := writer.WriteMessages(ctx, kafka.Message{Key: []byte(content.String()), Value: value}); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	consumerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	consumer := NewContentOwnershipConsumer(pgstore.New(pool))
	go consumer.Start(consumerCtx, []string{broker}, topic, nil)
	// A consumer group's first join is slow and not something the test can
	// make faster; the budget just has to be clear of it rather than
	// resting on it.
	deadline := time.Now().Add(90 * time.Second)
	projected := false
	for time.Now().Before(deadline) {
		ownership, err := pgstore.New(pool).GetContentOwnership(ctx, content)
		if err == nil {
			if ownership.CreatorID != creator || ownership.ContentType != postclassify.LongVideo {
				t.Fatalf("projection=%+v", ownership)
			}
			projected = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !projected {
		t.Fatal("PostCreated ownership was not projected")
	}

	// The transcode pipeline measures the video and post-service moves it
	// to the other side of shared/postclassify. That decision has to reach
	// this projection: content_type here chooses the display-view bar and
	// the RPM rate the creator fund settles at, so a stale label is a
	// mispriced payout, not a cosmetic one.
	changed := envelopeValue(t, ctx, events.PostContentTypeChanged, events.PostContentTypeChangedPayload{
		PostID: content.String(), AuthorID: creator.String(),
		OldType: postclassify.LongVideo, NewType: postclassify.Flick,
		ChangedAt: time.Now().UTC(),
	}, &creatorStr)
	writer = &kafka.Writer{Addr: kafka.TCP(broker), Topic: topic}
	if err := writer.WriteMessages(ctx, kafka.Message{Key: []byte(content.String()), Value: changed}); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		ownership, err := pgstore.New(pool).GetContentOwnership(ctx, content)
		if err == nil && ownership.ContentType == postclassify.Flick {
			if ownership.CreatorID != creator {
				t.Fatalf("reclassification moved the creator: %+v", ownership)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("PostContentTypeChanged did not reach the ownership projection")
}

// envelopeValue wraps a payload the way the producers do.
func envelopeValue(t *testing.T, ctx context.Context, eventType string, payload any, actor *string) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(events.NewEnvelope(ctx, eventType, actor, body))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
