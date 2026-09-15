//go:build integration

package service

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dating lane D4 end to end against real Postgres: two delivered messages in
// a dating conversation (replayed once, as the repair worker would) make one
// call to a fake dating-service, for the first sender.
func TestDatingFirstMessageDeliveredOnceThroughRealStore(t *testing.T) {
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing database %q: this test drops the chat schema", cfg.ConnConfig.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS chat CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}

	a, b := uuid.New(), uuid.New()
	convID, matchID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by, source_app, match_id) VALUES ($1, 'direct', $2, 'dating', $3)`, convID, a, matchID); err != nil {
		t.Fatal(err)
	}
	conversationStore := store.New(pool)
	dating := newFakeDating(t, http.StatusInternalServerError, http.StatusOK)
	svc := &Service{
		convStore:          conversationStore,
		msgStore:           &deliveryMessageStore{inboxWrites: map[uuid.UUID]int{}},
		log:                slog.Default(),
		internalServiceKey: testInternalKey,
		datingServiceURL:   dating.srv.URL,
		httpClient:         &http.Client{Timeout: 2 * time.Second},
	}

	for i, sender := range []uuid.UUID{a, b} {
		ts := time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		intent, err := conversationStore.ReserveMessageDeliveryIntent(ctx, store.MessageDeliveryIntent{
			IdempotencyKey: "d4-it-" + sender.String(), RequestHash: "h",
			ConversationID: convID, SenderID: sender, MessageID: uuid.New(),
			Bucket: ts.Format("200601"), MessageTS: ts, MessageType: "text", MessageText: "hi",
			MemberIDs: []uuid.UUID{a, b}, SourceApp: "dating", MatchID: &matchID,
		})
		if err != nil {
			t.Fatal(err)
		}
		for replay := 0; replay < 2; replay++ {
			if err := svc.completeMessageDelivery(ctx, intent); err != nil {
				t.Fatalf("delivery %d replay %d: %v", i, replay, err)
			}
		}
	}

	// Pass 1 gets a 500 and defers; make it due and run until quiet.
	for i := 0; i < 4; i++ {
		if err := svc.runDatingFirstMessagePass(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE chat.dating_first_message_notifications SET next_attempt_at = now() - interval '1 second' WHERE delivered_at IS NULL AND terminal_at IS NULL`); err != nil {
			t.Fatal(err)
		}
	}

	calls := dating.snapshot()
	if len(calls) != 2 {
		t.Fatalf("want 500 then 200 = two calls, got %d", len(calls))
	}
	want := "/v1/dating/internal/matches/" + matchID.String() + "/first-message"
	for _, c := range calls {
		if c.path != want || c.actor != a.String() || c.header.Get("X-User-Id") != "" || c.header.Get("X-Internal-Service-Key") != testInternalKey {
			t.Fatalf("bad call %+v", c)
		}
	}
	var delivered bool
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL, attempt_count FROM chat.dating_first_message_notifications WHERE conversation_id = $1`, convID).Scan(&delivered, &attempts); err != nil {
		t.Fatal(err)
	}
	if !delivered || attempts != 2 {
		t.Fatalf("delivered=%v attempts=%d", delivered, attempts)
	}
}
