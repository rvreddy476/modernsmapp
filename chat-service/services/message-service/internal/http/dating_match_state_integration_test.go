//go:build integration

package http

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	chatservice "github.com/atpost/chat-message-service/internal/service"
	pgstore "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDatingMatchStateEndToEnd drives the real middleware, handler, service
// and Postgres store. The answer here is what decides whether a matched pair
// can place a call, so it must track the SAME conversation row that gates
// their messages: open match true, closed match false, departed member false,
// unmatched pair false.
func TestDatingMatchStateEndToEnd(t *testing.T) {
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
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
	serviceHeaders := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}

	probe := func(t *testing.T, a, b uuid.UUID) bool {
		t.Helper()
		got := postDatingMatch(router, datingStatePath, datingStateBody(a, b), serviceHeaders)
		if got.Code != http.StatusOK {
			t.Fatalf("probe returned %d body=%s", got.Code, got.Body.String())
		}
		return openMatchFrom(t, got)
	}

	a, b := uuid.New(), uuid.New()
	stranger := uuid.New()

	// Refusals never reach the database and never answer.
	if got := postDatingMatch(router, datingStatePath, datingStateBody(a, b), nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous returned %d", got.Code)
	}
	userHeaders := map[string]string{
		"X-Internal-Service-Key": datingMatchTestKey,
		"Authorization":          "Bearer " + userToken(t, a),
		"X-User-Id":              a.String(),
		"X-Verified-User-Id":     a.String(),
		"X-Scopes":               "user",
	}
	if got := postDatingMatch(router, datingStatePath, datingStateBody(a, b), userHeaders); got.Code != http.StatusForbidden {
		t.Fatalf("user identity returned %d", got.Code)
	}

	// No match yet.
	if probe(t, a, b) {
		t.Fatal("unmatched pair reported an open match")
	}

	// Open match, both directions.
	matchID := uuid.New()
	if _, err := svc.CreateDatingMatchConversation(ctx, a, b, matchID); err != nil {
		t.Fatalf("create match conversation: %v", err)
	}
	if !probe(t, a, b) {
		t.Fatal("open match reported as closed (a->b)")
	}
	if !probe(t, b, a) {
		t.Fatal("open match reported as closed (b->a)")
	}

	// A third party is not in the match.
	if probe(t, a, stranger) {
		t.Fatal("stranger pair reported an open match")
	}

	// Closing the match closes calls in the same instant it closes chat.
	if err := svc.CloseDatingMatchConversation(ctx, matchID); err != nil {
		t.Fatalf("close match: %v", err)
	}
	if probe(t, a, b) {
		t.Fatal("closed match still reported as open")
	}

	// A departed member is not a live match either, even while the
	// conversation itself is open.
	c, d := uuid.New(), uuid.New()
	secondMatch := uuid.New()
	if _, err := svc.CreateDatingMatchConversation(ctx, c, d, secondMatch); err != nil {
		t.Fatalf("create second match conversation: %v", err)
	}
	if !probe(t, c, d) {
		t.Fatal("second open match reported as closed")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat.conversation_members m
		SET left_at = NOW()
		FROM chat.conversations c
		WHERE m.conversation_id = c.id AND c.match_id = $1 AND m.user_id = $2
	`, secondMatch, d); err != nil {
		t.Fatalf("mark member departed: %v", err)
	}
	if probe(t, c, d) {
		t.Fatal("match with a departed member reported as open")
	}
}
