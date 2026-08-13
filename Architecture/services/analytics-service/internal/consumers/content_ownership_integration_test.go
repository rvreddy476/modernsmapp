//go:build integration

package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/atpost/analytics-service/database"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

func TestLivePostCreatedProjectsCanonicalOwnership(t *testing.T) {
	dsn := os.Getenv("ANALYTICS_POSTGRES_DSN")
	broker := os.Getenv("KAFKA_BROKERS")
	if dsn == "" || broker == "" {
		t.Skip("ANALYTICS_POSTGRES_DSN and KAFKA_BROKERS are required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.ingest_receipts, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}

	content, creator := uuid.New(), uuid.New()
	payload := events.PostCreatedPayload{
		PostID: content.String(), AuthorID: creator.String(),
		ContentType: "long_video", CreatedAt: time.Now().UTC(),
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
	writer := &kafka.Writer{Addr: kafka.TCP(broker), Topic: topic, AllowAutoTopicCreation: true}
	if err := writer.WriteMessages(ctx, kafka.Message{Key: []byte(content.String()), Value: value}); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	consumerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	consumer := NewContentOwnershipConsumer(pgstore.New(pool))
	go consumer.Start(consumerCtx, []string{broker}, topic, nil)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ownership, err := pgstore.New(pool).GetContentOwnership(ctx, content)
		if err == nil {
			if ownership.CreatorID != creator || ownership.ContentType != "long_video" {
				t.Fatalf("projection=%+v", ownership)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("PostCreated ownership was not projected")
}
