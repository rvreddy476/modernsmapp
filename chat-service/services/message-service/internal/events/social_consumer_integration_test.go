//go:build integration

package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	pgstore "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

type retrySocialStore struct {
	failBlock bool
	attempts  atomic.Int32
	applied   chan struct{}
}

func (s *retrySocialStore) PromoteRequestConversationByPair(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

func (s *retrySocialStore) SeverDirectConversation(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	s.attempts.Add(1)
	if s.failBlock {
		return false, errors.New("injected durable-store failure")
	}
	select {
	case s.applied <- struct{}{}:
	default:
	}
	return true, nil
}

func TestFetchedOffsetSurvivesCrashBeforeDurableEffect(t *testing.T) {
	broker := os.Getenv("KAFKA_BROKERS")
	if broker == "" {
		t.Fatal("KAFKA_BROKERS is required")
	}
	topic := fmt.Sprintf("m5-social-durability-%d", time.Now().UnixNano())
	group := fmt.Sprintf("m5-social-group-%d", time.Now().UnixNano())
	blocker, blocked := uuid.New(), uuid.New()
	payload, _ := json.Marshal(socialEnvelope{EventType: socialUserBlocked, Payload: mustJSON(t, userBlockedPayload{BlockerID: blocker.String(), BlockedID: blocked.String()})})
	writer := &kafka.Writer{Addr: kafka.TCP(broker), Topic: topic, AllowAutoTopicCreation: true}
	if err := writer.WriteMessages(context.Background(), kafka.Message{Value: payload}); err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	failing := &retrySocialStore{failBlock: true, applied: make(chan struct{}, 1)}
	first := NewSocialConsumer([]string{broker}, topic, group, failing, nil)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan struct{})
	go func() { first.Start(firstCtx); close(firstDone) }()
	deadline := time.After(10 * time.Second)
	for failing.attempts.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("first consumer never attempted the message")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(15 * time.Second):
		t.Fatal("first consumer did not stop")
	}

	succeeding := &retrySocialStore{applied: make(chan struct{}, 1)}
	second := NewSocialConsumer([]string{broker}, topic, group, succeeding, nil)
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	go second.Start(secondCtx)
	select {
	case <-succeeding.applied:
	case <-time.After(10 * time.Second):
		t.Fatal("uncommitted block event was lost after consumer restart")
	}
}

func TestLiveConsumersApplyCanonicalPostgresEffects(t *testing.T) {
	broker := os.Getenv("KAFKA_BROKERS")
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if broker == "" || dsn == "" {
		t.Fatal("KAFKA_BROKERS and CHAT_POSTGRES_DSN are required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	store := pgstore.New(pool)

	t.Run("block sever", func(t *testing.T) {
		blocker, blocked := uuid.New(), uuid.New()
		conversationID, _, err := store.CreateDirectConversation(ctx, blocker, blocked, blocker)
		if err != nil {
			t.Fatal(err)
		}
		topic, group := uniqueKafkaNames("social-postgres")
		publishEnvelope(t, broker, topic, socialEnvelope{EventType: socialUserBlocked, Payload: mustJSON(t, userBlockedPayload{BlockerID: blocker.String(), BlockedID: blocked.String()})})
		consumer := NewSocialConsumer([]string{broker}, topic, group, store, nil)
		cancel := startLiveConsumer(t, consumer.Start)
		defer cancel()
		waitForPostgres(t, func() bool {
			var severedCount int
			err := pool.QueryRow(ctx, `
				SELECT count(*) FROM chat.conversation_members
				WHERE conversation_id=$1 AND user_id=ANY($2::uuid[]) AND left_at IS NOT NULL
			`, conversationID, []uuid.UUID{blocker, blocked}).Scan(&severedCount)
			return err == nil && severedCount == 2
		})
	})

	t.Run("identity upsert", func(t *testing.T) {
		userID := uuid.New()
		topic, group := uniqueKafkaNames("identity-postgres")
		publishEnvelope(t, broker, topic, identityEnvelope{EventType: identityUserProfileUpdated, Payload: mustJSON(t, userProfileUpdatedPayload{UserID: userID.String(), DisplayName: "Durable User"})})
		consumer := NewIdentityConsumer([]string{broker}, topic, group, store, nil)
		cancel := startLiveConsumer(t, consumer.Start)
		defer cancel()
		waitForPostgres(t, func() bool {
			var displayName string
			err := pool.QueryRow(ctx, `SELECT display_name FROM chat.user_profiles WHERE user_id=$1`, userID).Scan(&displayName)
			return err == nil && displayName == "Durable User"
		})
	})

	t.Run("dating close", func(t *testing.T) {
		userA, userB, matchID, conversationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO chat.conversations (id, type, created_by, source_app, match_id)
			VALUES ($1, 'direct', $2, 'dating', $3)
		`, conversationID, userA, matchID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO chat.conversation_members (conversation_id, user_id, role)
			VALUES ($1,$2,'member'),($1,$3,'member')
		`, conversationID, userA, userB); err != nil {
			t.Fatal(err)
		}
		topic, group := uniqueKafkaNames("dating-postgres")
		publishEnvelope(t, broker, topic, datingEnvelope{EventType: datingMatchClosed, Payload: mustJSON(t, matchClosedPayload{MatchID: matchID.String()})})
		consumer := NewDatingConsumer([]string{broker}, topic, group, store, nil)
		cancel := startLiveConsumer(t, consumer.Start)
		defer cancel()
		waitForPostgres(t, func() bool {
			var closed bool
			err := pool.QueryRow(ctx, `SELECT closed_at IS NOT NULL FROM chat.conversations WHERE id=$1`, conversationID).Scan(&closed)
			return err == nil && closed
		})
	})
}

func uniqueKafkaNames(prefix string) (string, string) {
	suffix := time.Now().UnixNano()
	return fmt.Sprintf("m5-%s-%d", prefix, suffix), fmt.Sprintf("m5-%s-group-%d", prefix, suffix)
}

func publishEnvelope(t *testing.T, broker, topic string, envelope any) {
	t.Helper()
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	writer := &kafka.Writer{Addr: kafka.TCP(broker), Topic: topic, AllowAutoTopicCreation: true}
	defer writer.Close()
	if err := writer.WriteMessages(context.Background(), kafka.Message{Value: payload}); err != nil {
		t.Fatal(err)
	}
}

func startLiveConsumer(t *testing.T, start func(context.Context)) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go start(ctx)
	return func() {
		cancel()
		time.Sleep(200 * time.Millisecond)
	}
}

func waitForPostgres(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("canonical PostgreSQL effect did not become durable")
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
