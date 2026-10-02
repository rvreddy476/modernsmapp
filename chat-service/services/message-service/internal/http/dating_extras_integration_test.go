//go:build integration

package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	chatservice "github.com/atpost/chat-message-service/internal/service"
	pgstore "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// TestDatingMatchExtrasEndToEnd (dating mechanic M9) drives the real routes,
// service and Postgres store: a gated conversation discloses read receipts
// only to a member dating allows, and the open-match answer graph grants
// calls on waits until both members have written.
func TestDatingMatchExtrasEndToEnd(t *testing.T) {
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Drops the chat schema: only ever a scratch *_test database.
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
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
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	var rdb *redis.Client
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		rdb = redis.NewClient(&redis.Options{Addr: addr, DB: 15})
		defer rdb.Close()
	}
	store := pgstore.New(pool)
	svc := chatservice.New(store, nil, rdb, nil, slog.Default(), time.Second)
	router := newDatingMatchRouter(t, svc)
	service := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}
	a, b, match := uuid.New(), uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]any{"user_a": a.String(), "user_b": b.String(), "match_id": match.String(),
		"receipts_gated": true, "call_after_exchange": true})
	got := postDatingMatch(router, datingMatchPath, string(body), service)
	if got.Code != http.StatusOK {
		t.Fatalf("create: %d %s", got.Code, got.Body.String())
	}
	convID := uuid.MustParse(conversationIDFrom(t, got))

	// Calls: closed until both have written.
	open := func() bool {
		ok, err := svc.HasOpenDatingMatch(ctx, a, b)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if open() {
		t.Fatalf("a gated match is open for calls before anyone wrote")
	}
	if err := store.MarkMemberSent(ctx, convID, a); err != nil {
		t.Fatal(err)
	}
	if open() {
		t.Fatalf("open for calls after only one member wrote")
	}
	if err := store.MarkMemberSent(ctx, convID, b); err != nil {
		t.Fatal(err)
	}
	if !open() {
		t.Fatalf("still closed for calls after both wrote")
	}

	// Read receipts: B has read; A sees it only while dating allows.
	if _, err := pool.Exec(ctx, `INSERT INTO chat.user_policy (user_id, read_receipts_visibility, refreshed_at) VALUES ($1, 'everyone', now())`, b); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO chat.read_cursors (conversation_id, user_id, last_read_message_id, last_read_at, updated_at)
        VALUES ($1, $2, $3, now(), now())`, convID, b, uuid.New()); err != nil {
		t.Fatal(err)
	}
	readAtSeen := func() bool {
		conv, err := svc.GetConversation(ctx, a, convID)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range conv.Members {
			if m.UserID == b {
				return m.LastReadAt != nil
			}
		}
		t.Fatalf("member b missing")
		return false
	}
	if readAtSeen() {
		t.Fatalf("a gated conversation disclosed b's read state to a without dating's allowance")
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	rr := `{"match_id":"` + match.String() + `","user_id":"` + a.String() + `","until":"` + future + `"}`
	if got := postDatingMatch(router, datingMatchPath+"/read-receipts", rr, service); got.Code != http.StatusOK {
		t.Fatalf("allow receipts: %d %s", got.Code, got.Body.String())
	}
	if !readAtSeen() {
		t.Fatalf("allowed member still sees no read state")
	}
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	rr = `{"match_id":"` + match.String() + `","user_id":"` + a.String() + `","until":"` + past + `"}`
	if got := postDatingMatch(router, datingMatchPath+"/read-receipts", rr, service); got.Code != http.StatusOK {
		t.Fatalf("expire receipts: %d", got.Code)
	}
	if readAtSeen() {
		t.Fatalf("an expired allowance still discloses read state")
	}

	// The live frame follows the same rule (needs Redis).
	if rdb != nil {
		sub := rdb.Subscribe(ctx, "chat:"+a.String())
		defer sub.Close()
		if _, err := sub.Receive(ctx); err != nil {
			t.Fatal(err)
		}
		// The channel, not ReceiveMessage with a deadline: a timed-out read
		// can leave the subscription unusable for the next wait.
		frames := sub.Channel()
		frame := func() bool {
			if err := svc.MarkRead(ctx, b, convID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			select {
			case msg := <-frames:
				return strings.Contains(msg.Payload, `"read_receipt"`)
			case <-time.After(700 * time.Millisecond):
				return false
			}
		}
		if frame() {
			t.Fatalf("a read_receipt frame reached a member dating does not allow")
		}
		rr = `{"match_id":"` + match.String() + `","user_id":"` + a.String() + `","until":"` + future + `"}`
		if got := postDatingMatch(router, datingMatchPath+"/read-receipts", rr, service); got.Code != http.StatusOK {
			t.Fatalf("allow receipts: %d", got.Code)
		}
		if !frame() {
			t.Fatalf("no read_receipt frame for an allowed member")
		}
	}

	// An ungated dating conversation is unchanged on both counts.
	c, d, plain := uuid.New(), uuid.New(), uuid.New()
	if got := postDatingMatch(router, datingMatchPath, datingMatchBody(c, d, plain), service); got.Code != http.StatusOK {
		t.Fatalf("plain create: %d", got.Code)
	}
	if ok, _ := svc.HasOpenDatingMatch(ctx, c, d); !ok {
		t.Fatalf("an ungated match is not open for calls")
	}

	// The read-receipts route is service-only and 404s for a stranger.
	if got := postDatingMatch(router, datingMatchPath+"/read-receipts", rr, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read-receipts returned %d", got.Code)
	}
	stranger := `{"match_id":"` + match.String() + `","user_id":"` + uuid.NewString() + `"}`
	if got := postDatingMatch(router, datingMatchPath+"/read-receipts", stranger, service); got.Code != http.StatusNotFound {
		t.Fatalf("read-receipts for a non-member returned %d", got.Code)
	}
}
