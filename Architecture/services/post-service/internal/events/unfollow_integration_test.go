//go:build integration

package events

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserUnfollowed against a real Postgres (2026-09-12): the consumer's
// handler, wired to the real store, removes the row and enqueues
// tube.channel.unsubscribed. No Kafka needed; the envelope is handed to the
// handler directly, which is exactly what handleUntilDurable does.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/events/ -run Unfollow -v

// subscriptionTestPool refuses any database whose name does not end in
// _test: this writes channels, subscriptions and outbox rows for real.
func subscriptionTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse: %v", err)
	}
	switch db := cfg.Database; {
	case db == "" || db == "app" || !strings.HasSuffix(db, "_test"):
		t.Fatalf("refusing to run integration tests against database %q: use a scratch database whose name ends in _test (e.g. post_it_test)", db)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Cross-service parent tables post-service's migrations point at
	// (user-service boots first; see the store suite for why).
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS user_preferences (user_id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id),
			handle TEXT NOT NULL UNIQUE, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
			subscriber_count INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL, moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS post_outbox_events (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(), event_type TEXT NOT NULL,
			aggregate_type TEXT NOT NULL, aggregate_id UUID NOT NULL, payload JSONB NOT NULL,
			published BOOLEAN NOT NULL DEFAULT FALSE, published_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestUserUnfollowedRemovesTheRowIntegration(t *testing.T) {
	pool := subscriptionTestPool(t)
	ctx := context.Background()
	st := postgres.New(pool)

	owner, follower := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{owner, follower} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
			t.Fatal(err)
		}
	}
	ch := &postgres.Channel{UserID: owner, Name: "Unfollow Proof", Handle: "unfollow." + strings.ToLower(uuid.NewString()[:8])}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, ch.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id = $1`, ch.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{owner, follower})
	})
	if _, err := st.Subscribe(ctx, ch.ID, follower, postgres.NotifyOnAll); err != nil {
		t.Fatal(err)
	}

	c := (&Consumer{db: pool}).WithSubscriptionStore(st)
	payload, _ := json.Marshal(events.UserUnfollowedPayload{FollowerID: follower.String(), FolloweeID: owner.String(), OccurredAt: time.Now()})
	if err := c.handleUserUnfollowed(ctx, payload); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if sub, err := st.GetSubscription(ctx, ch.ID, follower); err != nil || sub != nil {
		t.Fatalf("row after unfollow: %+v %v, want gone", sub, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_outbox_events WHERE aggregate_id = $1 AND event_type = $2`,
		ch.ID, events.TubeChannelUnsubscribed).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("tube.channel.unsubscribed events = %d, want 1", n)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT subscriber_count FROM channels WHERE id = $1`, ch.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("subscriber_count = %d after unfollow, want 0", count)
	}
	// Redelivery is a no-op.
	if err := c.handleUserUnfollowed(ctx, payload); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
}
