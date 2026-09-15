//go:build integration

package http

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	chatservice "github.com/atpost/chat-message-service/internal/service"
	pgstore "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDatingMatchConversationEndToEnd drives the real middleware, handler,
// service and Postgres store: one conversation per match_id, between exactly
// the requested pair, and a refusal when the match_id is reused for another.
func TestDatingMatchConversationEndToEnd(t *testing.T) {
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

	svc := chatservice.New(pgstore.New(pool), nil, nil, nil, slog.Default(), time.Second)
	router := newDatingMatchRouter(t, svc)
	a, b, match := uuid.New(), uuid.New(), uuid.New()
	service := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}

	// Refusals never touch the database.
	if got := postDatingMatch(router, datingMatchPath, datingMatchBody(a, b, match), nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous returned %d", got.Code)
	}
	userHeaders := map[string]string{"X-Internal-Service-Key": datingMatchTestKey, "Authorization": "Bearer " + userToken(t, a), "X-User-Id": a.String(), "X-Verified-User-Id": a.String(), "X-Scopes": "user"}
	if got := postDatingMatch(router, datingMatchPath, datingMatchBody(a, b, match), userHeaders); got.Code != http.StatusForbidden {
		t.Fatalf("user identity returned %d", got.Code)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM chat.conversations WHERE match_id = $1`, match).Scan(&count); err != nil || count != 0 {
		t.Fatalf("refused calls created %d conversations (err=%v)", count, err)
	}

	first := postDatingMatch(router, datingMatchPath, datingMatchBody(a, b, match), service)
	if first.Code != http.StatusOK && first.Code != http.StatusCreated {
		t.Fatalf("service call returned %d body=%s", first.Code, first.Body.String())
	}
	second := postDatingMatch(router, datingMatchPath, datingMatchBody(b, a, match), service)
	if second.Code != http.StatusOK && second.Code != http.StatusCreated {
		t.Fatalf("repeat call returned %d body=%s", second.Code, second.Body.String())
	}
	firstID, secondID := conversationIDFrom(t, first), conversationIDFrom(t, second)
	if firstID == "" || firstID != secondID {
		t.Fatalf("repeat returned a different conversation: %q vs %q", firstID, secondID)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM chat.conversations WHERE match_id = $1 AND source_app = 'dating'`, match).Scan(&count); err != nil || count != 1 {
		t.Fatalf("want 1 conversation for the match, got %d (err=%v)", count, err)
	}

	rows, err := pool.Query(ctx, `SELECT user_id::text FROM chat.conversation_members WHERE conversation_id = $1`, firstID)
	if err != nil {
		t.Fatal(err)
	}
	var members []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		members = append(members, id)
	}
	rows.Close()
	want := []string{a.String(), b.String()}
	sort.Strings(members)
	sort.Strings(want)
	if len(members) != 2 || members[0] != want[0] || members[1] != want[1] {
		t.Fatalf("members = %v, want exactly %v", members, want)
	}

	// Reusing the match_id for a different pair must not hand out the
	// existing conversation.
	other := postDatingMatch(router, datingMatchPath, datingMatchBody(a, uuid.New(), match), service)
	if other.Code != http.StatusConflict {
		t.Fatalf("match_id reused for another pair returned %d body=%s", other.Code, other.Body.String())
	}
}
