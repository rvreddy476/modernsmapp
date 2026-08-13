//go:build integration

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/atpost/group-service/database"
	"github.com/atpost/group-service/internal/store"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

func TestLiveDeletionConsumerAppliesEveryCanonicalEffect(t *testing.T) {
	broker := os.Getenv("KAFKA_BROKERS")
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if broker == "" || dsn == "" {
		t.Fatal("KAFKA_BROKERS and GROUP_POSTGRES_DSN are required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	// These are canonical migration-001 columns/tables. The isolated suite
	// does not run unrelated group-post migrations that require post-service's
	// shared tables.
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removed_at TIMESTAMPTZ;
		CREATE TABLE IF NOT EXISTS group_join_requests (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			reviewed_at TIMESTAMPTZ
		);
	`); err != nil {
		t.Fatal(err)
	}

	userID, groupID, otherID := uuid.New(), uuid.New(), uuid.New()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO groups (id, name, creator_id) VALUES ($1, 'Deletion durability', $2)`, []any{groupID, userID}},
		{`INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1,$2,'admin','active')`, []any{groupID, userID}},
		{`INSERT INTO group_invites (group_id, inviter_id, invitee_id, status) VALUES ($1,$2,$3,'pending')`, []any{groupID, otherID, userID}},
		{`INSERT INTO group_join_requests (group_id, user_id, status) VALUES ($1,$2,'pending')`, []any{groupID, userID}},
	} {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	payload, _ := json.Marshal(sharedevents.UserDeletionRequestedPayload{UserID: userID.String(), RequestedAt: time.Now()})
	envelope, _ := json.Marshal(sharedevents.EventEnvelope{
		EventID: uuid.NewString(), EventType: sharedevents.EventUserDeletionRequested,
		OccurredAt: time.Now(), Payload: payload,
	})
	writer := &kafka.Writer{Addr: kafka.TCP(broker), Topic: "platform-events", AllowAutoTopicCreation: true}
	if err := writer.WriteMessages(ctx, kafka.Message{Key: []byte(userID.String()), Value: envelope}); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()

	consumer := NewConsumer([]string{broker}, fmt.Sprintf("m5-group-delete-%d", time.Now().UnixNano()), store.New(pool), nil)
	consumerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go consumer.Start(consumerCtx)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var groupStatus, memberStatus, inviteStatus, requestStatus string
		err := pool.QueryRow(ctx, `SELECT status FROM groups WHERE id=$1`, groupID).Scan(&groupStatus)
		if err == nil {
			err = pool.QueryRow(ctx, `SELECT status FROM group_members WHERE group_id=$1 AND user_id=$2`, groupID, userID).Scan(&memberStatus)
		}
		if err == nil {
			err = pool.QueryRow(ctx, `SELECT status FROM group_invites WHERE group_id=$1 AND invitee_id=$2`, groupID, userID).Scan(&inviteStatus)
		}
		if err == nil {
			err = pool.QueryRow(ctx, `SELECT status FROM group_join_requests WHERE group_id=$1 AND user_id=$2`, groupID, userID).Scan(&requestStatus)
		}
		if err == nil && groupStatus == "archived" && memberStatus == "removed" && inviteStatus == "rejected" && requestStatus == "rejected" {
			cancel()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("group account-deletion effects did not all become durable")
}
