//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

// TestLivePoisonOffsetWaitsForDurableQuarantine proves the ordering against a
// real group coordinator: no quarantine table means handleUntilDurable refuses
// commit; after the same bytes are durable, the committed offset advances.
func TestLivePoisonOffsetWaitsForDurableQuarantine(t *testing.T) {
	dsn := os.Getenv("MEDIA_DURABILITY_POSTGRES_DSN")
	broker := os.Getenv("KAFKA_BROKERS")
	if dsn == "" || broker == "" {
		t.Skip("MEDIA_DURABILITY_POSTGRES_DSN and KAFKA_BROKERS are required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	store := postgres.New(pool)

	suffix := uuid.NewString()
	topic := "m4-media-poison-" + suffix
	group := "m4-media-worker-" + suffix
	writer := &kafka.Writer{Addr: kafka.TCP(broker), Topic: topic, AllowAutoTopicCreation: true}
	if err := writer.WriteMessages(ctx, kafka.Message{Key: []byte("poison"), Value: []byte("{not-json")}); err != nil {
		t.Fatalf("write poison: %v", err)
	}
	defer writer.Close()
	reader := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{broker}, GroupID: group, Topic: topic, MinBytes: 1, MaxBytes: 1e6})
	defer reader.Close()
	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	m, err := reader.FetchMessage(fetchCtx)
	cancel()
	if err != nil {
		t.Fatalf("fetch poison: %v", err)
	}

	if _, err := pool.Exec(ctx, `ALTER TABLE media_transcode_quarantine RENAME TO media_transcode_quarantine_offline`); err != nil {
		t.Fatalf("disable quarantine sink: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `ALTER TABLE IF EXISTS media_transcode_quarantine_offline RENAME TO media_transcode_quarantine`)
	}()
	blockedCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
	mayCommit := handleUntilDurable(blockedCtx, m, store, nil, processing.Scanner(nil))
	stop()
	if mayCommit {
		t.Fatal("poison message was declared committable without a durable quarantine row")
	}
	if got := committedOffset(t, broker, group, topic, m.Partition); got >= m.Offset+1 {
		t.Fatalf("offset advanced before quarantine: got %d message=%d", got, m.Offset)
	}

	if _, err := pool.Exec(ctx, `ALTER TABLE media_transcode_quarantine_offline RENAME TO media_transcode_quarantine`); err != nil {
		t.Fatalf("restore quarantine sink: %v", err)
	}
	if !handleUntilDurable(ctx, m, store, nil, processing.Scanner(nil)) {
		t.Fatal("durably quarantined poison was not committable")
	}
	if err := reader.CommitMessages(ctx, m); err != nil {
		t.Fatalf("commit: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := committedOffset(t, broker, group, topic, m.Partition); got == m.Offset+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("committed offset did not reach %d", m.Offset+1)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_transcode_quarantine WHERE topic=$1 AND partition_id=$2 AND offset_id=$3`, topic, m.Partition, m.Offset).Scan(&count); err != nil || count != 1 {
		t.Fatalf("quarantine row count=%d err=%v", count, err)
	}
}

func committedOffset(t *testing.T, broker, group, topic string, partition int) int64 {
	t.Helper()
	client := &kafka.Client{Addr: kafka.TCP(broker)}
	resp, err := client.OffsetFetch(context.Background(), &kafka.OffsetFetchRequest{
		GroupID: group,
		Topics:  map[string][]int{topic: {partition}},
	})
	if err != nil {
		t.Fatalf("offset fetch: %v", err)
	}
	parts := resp.Topics[topic]
	if len(parts) != 1 || parts[0].Error != nil {
		t.Fatalf("offset response for %s/%d: %+v (%v)", topic, partition, parts, resp.Error)
	}
	if resp.Error != nil {
		t.Fatalf("offset fetch response: %v", resp.Error)
	}
	return parts[0].CommittedOffset
}
