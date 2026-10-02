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

// TestDatingTurnsOwedEndToEnd (dating mechanic M11): a reply is owed by the
// member who did not send the newest message, only while the match is open
// and both are still in it.
func TestDatingTurnsOwedEndToEnd(t *testing.T) {
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
	store := pgstore.New(pool)
	svc := chatservice.New(store, nil, nil, nil, slog.Default(), time.Second)
	router := newDatingMatchRouter(t, svc)
	key := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}

	newMatch := func(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
		match := uuid.New()
		body, _ := json.Marshal(map[string]any{"user_a": a.String(), "user_b": b.String(), "match_id": match.String()})
		got := postDatingMatch(router, datingMatchPath, string(body), key)
		if got.Code != http.StatusOK {
			t.Fatalf("create: %d %s", got.Code, got.Body.String())
		}
		return uuid.MustParse(conversationIDFrom(t, got)), match
	}
	owed := func(user uuid.UUID) int {
		got := postDatingMatch(router, datingTurnsPath, `{"user_id":"`+user.String()+`"}`, key)
		if got.Code != http.StatusOK {
			t.Fatalf("turns: %d %s", got.Code, got.Body.String())
		}
		return owedFrom(t, got)
	}
	say := func(conv, sender uuid.UUID) {
		if err := store.SetLastMessage(ctx, conv, sender, "hi", time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	a, b, c := uuid.New(), uuid.New(), uuid.New()
	ab, _ := newMatch(a, b)
	ac, acMatch := newMatch(a, c)
	if owed(a) != 0 {
		t.Fatalf("no messages yet, but a owes %d", owed(a))
	}
	say(ab, b)
	say(ac, c)
	if owed(a) != 2 || owed(b) != 0 || owed(c) != 0 {
		t.Fatalf("owed a=%d b=%d c=%d, want 2 0 0", owed(a), owed(b), owed(c))
	}
	say(ab, a)
	if owed(a) != 1 || owed(b) != 1 {
		t.Fatalf("after a replied: a=%d b=%d, want 1 1", owed(a), owed(b))
	}
	// A closed match owes nothing.
	if err := svc.CloseDatingMatchConversation(ctx, acMatch); err != nil {
		t.Fatal(err)
	}
	if owed(a) != 0 {
		t.Fatalf("a closed match still counts: %d", owed(a))
	}
	// Neither does one the other member left.
	if _, err := pool.Exec(ctx, `UPDATE chat.conversation_members SET left_at = now() WHERE conversation_id = $1 AND user_id = $2`, ab, a); err != nil {
		t.Fatal(err)
	}
	if owed(b) != 0 {
		t.Fatalf("a conversation the other member left still counts: %d", owed(b))
	}
	// Only dating conversations count.
	if _, err := pool.Exec(ctx, `UPDATE chat.conversation_members SET left_at = NULL WHERE conversation_id = $1`, ab); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE chat.conversations SET source_app = 'chat' WHERE id = $1`, ab); err != nil {
		t.Fatal(err)
	}
	if owed(b) != 0 {
		t.Fatalf("a plain chat counts as a dating turn: %d", owed(b))
	}
}
