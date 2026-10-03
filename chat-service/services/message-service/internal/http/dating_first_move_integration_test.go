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
)

// TestDatingFirstMoveEndToEnd (dating mechanic M5) drives the real route,
// service and Postgres store: first_mover_ids are stored on create, a retry
// re-applies them only while the conversation has no message, and the
// opening-answer route is service-only.
func TestDatingFirstMoveEndToEnd(t *testing.T) {
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required")
	}
	// This test drops the chat schema: never against anything but a scratch
	// *_test database.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
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
	store := pgstore.New(pool)
	svc := chatservice.New(store, nil, nil, nil, slog.Default(), time.Second)
	router := newDatingMatchRouter(t, svc)
	service := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}
	a, b, match := uuid.New(), uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]any{"user_a": a.String(), "user_b": b.String(), "match_id": match.String(), "first_mover_ids": []string{a.String()}})
	got := postDatingMatch(router, datingMatchPath, string(body), service)
	if got.Code != http.StatusOK {
		t.Fatalf("create: %d %s", got.Code, got.Body.String())
	}
	convID, err := uuid.Parse(conversationIDFrom(t, got))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.GetConversationMeta(ctx, convID)
	if err != nil || len(meta.FirstMovers) != 1 || meta.FirstMovers[0] != a || meta.LastMessageAt != nil {
		t.Fatalf("meta after create = %+v err=%v, want first mover %s and no message", meta, err, a)
	}
	if id, err := store.DatingConversationByMatch(ctx, match); err != nil || id != convID {
		t.Fatalf("conversation by match = %s err=%v", id, err)
	}

	// After the first message, a retry cannot put the rule back.
	if _, err := pool.Exec(ctx, `UPDATE chat.conversations SET last_message_at = now() WHERE id = $1`, convID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDatingFirstMovers(ctx, convID, []uuid.UUID{b}); err != nil {
		t.Fatal(err)
	}
	if meta, _ = store.GetConversationMeta(ctx, convID); len(meta.FirstMovers) != 1 || meta.FirstMovers[0] != a {
		t.Fatalf("first movers changed after the first message: %+v", meta.FirstMovers)
	}

	// A conversation created without the field has no rule.
	plain := uuid.New()
	if got := postDatingMatch(router, datingMatchPath, datingMatchBody(uuid.New(), uuid.New(), plain), service); got.Code != http.StatusOK {
		t.Fatalf("plain create: %d", got.Code)
	}
	plainID, _ := store.DatingConversationByMatch(ctx, plain)
	if meta, _ = store.GetConversationMeta(ctx, plainID); len(meta.FirstMovers) != 0 {
		t.Fatalf("a conversation created without first movers has %v", meta.FirstMovers)
	}

	// The opening-answer route refuses anonymous callers and user identity.
	answer := `{"match_id":"` + match.String() + `","sender_id":"` + b.String() + `","text":"hi","idempotency_key":"k1"}`
	if got := postDatingMatch(router, datingMatchPath+"/opening-answer", answer, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous opening answer returned %d", got.Code)
	}
	userHeaders := map[string]string{"X-Internal-Service-Key": datingMatchTestKey, "X-User-Id": b.String()}
	if got := postDatingMatch(router, datingMatchPath+"/opening-answer", answer, userHeaders); got.Code != http.StatusForbidden {
		t.Fatalf("opening answer with a user identity returned %d", got.Code)
	}
	// And refuses once the conversation has a message.
	if got := postDatingMatch(router, datingMatchPath+"/opening-answer", answer, service); got.Code != http.StatusConflict {
		t.Fatalf("opening answer after the first message returned %d %s", got.Code, got.Body.String())
	}
}
