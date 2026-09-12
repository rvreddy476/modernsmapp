//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tube channel subscriptions against a real Postgres (2026-09-12).
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run ChannelSubscription -v
//
// Runs the real schema path (setup.sql + every migration), so migration 046
// itself is under test: the two-value CHECK, the keyset index, the
// subscriber_count trigger, and the users FK when a users table exists.
// These run against real SQL because the fix IS the SQL: the fan-out
// query's notify_on filter is what user-service got wrong.

// requireTestDSN refuses any database whose name does not end in _test.
// Every test here writes channels, subscriptions and outbox rows through
// the real store; pointed at the live app database it would leave
// fixtures in the storefront. Same guard as monetization-service.
func requireTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse: %v", err)
	}
	db := cfg.Database
	switch {
	case db == "":
		t.Fatalf("refusing to run integration tests: POSTGRES_DSN names no database; use a scratch database whose name ends in _test (e.g. post_it_test)")
	case db == "app":
		t.Fatalf("refusing to run integration tests against the live database %q; use a scratch database whose name ends in _test (e.g. post_it_test)", db)
	case !strings.HasSuffix(db, "_test"):
		t.Fatalf("refusing to run integration tests against database %q: the name must end in _test (e.g. post_it_test)", db)
	}
	return dsn
}

// userServiceChannelsDDL is the channels table as user-service's phase-6
// DDL creates it (the shape migration 041 documents and adapts).
const userServiceChannelsDDL = `
	CREATE TABLE IF NOT EXISTS channels (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id),
		handle TEXT NOT NULL UNIQUE, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
		category TEXT NOT NULL DEFAULT '', country TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '',
		contact_email TEXT NOT NULL DEFAULT '', collab_status TEXT NOT NULL DEFAULT 'closed',
		content_schedule TEXT NOT NULL DEFAULT '', subscriber_count INTEGER NOT NULL DEFAULT 0,
		is_verified BOOLEAN NOT NULL DEFAULT FALSE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`

func openSubscriptionDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Cross-service parent tables post-service's migrations point at:
	// users (010 on), user_preferences (009) and user-service's phase-6
	// channels table, which 012 references long before 041 adopts it. So
	// the real boot order is user-service first, and that is what this
	// bootstrap reproduces; 046 then runs against a users table and takes
	// its users-FK branch, so the suite exercises the real constraint.
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS user_preferences (user_id UUID PRIMARY KEY)`,
		userServiceChannelsDDL,
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

// subscriptionRig: one owner with a channel, N subscribers, all cleaned up.
type subscriptionRig struct {
	pool    *pgxpool.Pool
	store   *postgres.Store
	owner   uuid.UUID
	channel *postgres.Channel
}

func newSubscriptionRig(t *testing.T) *subscriptionRig {
	t.Helper()
	pool := openSubscriptionDB(t)
	ctx := context.Background()
	st := postgres.New(pool)
	owner := uuid.New()
	newUser(t, pool, owner)
	ch := &postgres.Channel{UserID: owner, Name: "Sub Proof " + uuid.NewString()[:6], Handle: "sub.proof." + strings.ToLower(uuid.NewString()[:8])}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() {
		// channel_subscriptions cascades from channels.
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, ch.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id = $1`, ch.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner)
	})
	return &subscriptionRig{pool: pool, store: st, owner: owner, channel: ch}
}

func newUser(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
}

func (r *subscriptionRig) outboxTypes(t *testing.T) []string {
	t.Helper()
	rows, err := r.pool.Query(context.Background(),
		`SELECT event_type FROM post_outbox_events WHERE aggregate_id = $1 ORDER BY created_at, id`, r.channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (r *subscriptionRig) subscriberCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(), `SELECT subscriber_count FROM channels WHERE id = $1`, r.channel.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestChannelSubscriptionCheckRejectsHighlights(t *testing.T) {
	r := newSubscriptionRig(t)
	user := uuid.New()
	newUser(t, r.pool, user)
	for _, bad := range []string{"highlights", "uploads"} {
		_, err := r.pool.Exec(context.Background(),
			`INSERT INTO channel_subscriptions (channel_id, user_id, notify_on) VALUES ($1, $2, $3)`, r.channel.ID, user, bad)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("notify_on=%q: err = %v, want CHECK violation 23514", bad, err)
		}
	}
	// The migration is safe to run against a table that already carries
	// user-service's three-way CHECK: the swap is what this proves.
	var def string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'channel_subscriptions_notify_on_check'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(def, "highlights") {
		t.Fatalf("CHECK still admits 'highlights': %s", def)
	}
}

func TestChannelSubscriptionSubscribeWritesOutboxInSameTx(t *testing.T) {
	r := newSubscriptionRig(t)
	ctx := context.Background()
	user := uuid.New()
	newUser(t, r.pool, user)

	inserted, err := r.store.Subscribe(ctx, r.channel.ID, user, postgres.NotifyOnNone)
	if err != nil || !inserted {
		t.Fatalf("subscribe: inserted=%v err=%v", inserted, err)
	}
	if got := r.outboxTypes(t); len(got) != 1 || got[0] != events.TubeChannelSubscribed {
		t.Fatalf("outbox = %v, want [%s]", got, events.TubeChannelSubscribed)
	}
	if n := r.subscriberCount(t); n != 1 {
		t.Fatalf("subscriber_count = %d after subscribe, want 1 (trigger)", n)
	}

	// A retried tap: no row change, no bell reset, no second event.
	again, err := r.store.Subscribe(ctx, r.channel.ID, user, postgres.NotifyOnAll)
	if err != nil || again {
		t.Fatalf("second subscribe: inserted=%v err=%v, want false", again, err)
	}
	sub, err := r.store.GetSubscription(ctx, r.channel.ID, user)
	if err != nil || sub == nil || sub.NotifyOn != postgres.NotifyOnNone {
		t.Fatalf("bell after retry: %+v %v, want none kept", sub, err)
	}
	if got := r.outboxTypes(t); len(got) != 1 {
		t.Fatalf("outbox after retry = %v, want still one event", got)
	}

	// The payload names the owner so consumers can key on the follow edge.
	var payload events.ChannelSubscriptionPayload
	if err := r.pool.QueryRow(ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id = $1`, r.channel.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.OwnerID != r.owner.String() || payload.SubscriberID != user.String() || payload.NotifyOn != "none" || payload.ChannelID != r.channel.ID.String() {
		t.Fatalf("payload %+v", payload)
	}

	deleted, err := r.store.Unsubscribe(ctx, r.channel.ID, user)
	if err != nil || !deleted {
		t.Fatalf("unsubscribe: deleted=%v err=%v", deleted, err)
	}
	if got := r.outboxTypes(t); len(got) != 2 || got[1] != events.TubeChannelUnsubscribed {
		t.Fatalf("outbox = %v", got)
	}
	if n := r.subscriberCount(t); n != 0 {
		t.Fatalf("subscriber_count = %d after unsubscribe, want 0", n)
	}
	if deleted, _ := r.store.Unsubscribe(ctx, r.channel.ID, user); deleted {
		t.Fatal("second unsubscribe reported a deletion")
	}
	if err := r.store.SetNotifyOn(ctx, r.channel.ID, user, postgres.NotifyOnAll); !errors.Is(err, postgres.ErrNotSubscribed) {
		t.Fatalf("SetNotifyOn without a row: %v, want ErrNotSubscribed", err)
	}
}

func TestChannelSubscriptionListSubscriberIDsExcludesBellOffAndPages(t *testing.T) {
	r := newSubscriptionRig(t)
	ctx := context.Background()

	var bellOn []uuid.UUID
	for i := 0; i < 5; i++ {
		u := uuid.New()
		newUser(t, r.pool, u)
		if _, err := r.store.Subscribe(ctx, r.channel.ID, u, postgres.NotifyOnAll); err != nil {
			t.Fatal(err)
		}
		bellOn = append(bellOn, u)
	}
	bellOff := uuid.New()
	newUser(t, r.pool, bellOff)
	if _, err := r.store.Subscribe(ctx, r.channel.ID, bellOff, postgres.NotifyOnNone); err != nil {
		t.Fatal(err)
	}
	if n := r.subscriberCount(t); n != 6 {
		t.Fatalf("subscriber_count = %d, want 6 (bell state does not change membership)", n)
	}

	// Page in user_id order with limit 2: three pages, the third short.
	// A full page means "maybe more" (the handler's has_more), so the
	// caller loops until a short page.
	const limit = 2
	var seen []uuid.UUID
	after := uuid.Nil
	pages := 0
	for {
		ids, err := r.store.ListSubscriberIDsAfter(ctx, r.channel.ID, after, limit)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for i := 1; i < len(ids); i++ {
			if ids[i-1].String() >= ids[i].String() {
				t.Fatalf("page not in user_id order: %v", ids)
			}
		}
		seen = append(seen, ids...)
		if len(ids) < limit {
			break
		}
		after = ids[len(ids)-1]
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != 5 {
		t.Fatalf("fan-out saw %d subscribers, want the 5 with the bell on", len(seen))
	}
	for _, id := range seen {
		if id == bellOff {
			t.Fatal("bell-off subscriber included in the push fan-out")
		}
	}
	if pages != 3 {
		t.Fatalf("pages = %d, want 3 (2 + 2 + 1)", pages)
	}

	// The bell-off subscriber still belongs to the subscriptions feed.
	owners, err := r.store.ListSubscribedOwnersAfter(ctx, bellOff, uuid.Nil, 10)
	if err != nil || len(owners) != 1 || owners[0] != r.owner {
		t.Fatalf("owners for bell-off subscriber = %v %v, want [%s]", owners, err, r.owner)
	}

	// And the viewer's own page carries the bell and the channel.
	rows, err := r.store.ListSubscriptionsForUser(ctx, bellOff, nil, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v %v", rows, err)
	}
	if rows[0].NotifyOn != "none" || rows[0].Channel.ID != r.channel.ID || rows[0].Channel.SubscriberCount != 6 {
		t.Fatalf("row %+v", rows[0])
	}
	// Keyset past the only row is empty.
	next, err := r.store.ListSubscriptionsForUser(ctx, bellOff, &postgres.SubscriptionCursor{SubscribedAt: rows[0].SubscribedAt, ChannelID: rows[0].Channel.ID}, 10)
	if err != nil || len(next) != 0 {
		t.Fatalf("page after the last row = %+v %v", next, err)
	}
}

// The UserUnfollowed consumer's write: the follower's subscription to the
// followee's channel is removed and tube.channel.unsubscribed enqueued.
func TestChannelSubscriptionDeleteByOwnerOnUnfollow(t *testing.T) {
	r := newSubscriptionRig(t)
	ctx := context.Background()
	follower := uuid.New()
	newUser(t, r.pool, follower)
	if _, err := r.store.Subscribe(ctx, r.channel.ID, follower, postgres.NotifyOnAll); err != nil {
		t.Fatal(err)
	}

	deleted, err := r.store.DeleteSubscriptionByOwner(ctx, r.owner, follower)
	if err != nil || !deleted {
		t.Fatalf("delete by owner: deleted=%v err=%v", deleted, err)
	}
	if sub, _ := r.store.GetSubscription(ctx, r.channel.ID, follower); sub != nil {
		t.Fatal("row survived the unfollow")
	}
	if got := r.outboxTypes(t); len(got) != 2 || got[1] != events.TubeChannelUnsubscribed {
		t.Fatalf("outbox = %v", got)
	}
	// Redelivery, an unfollow of someone who never subscribed, and an
	// unfollow of someone with no channel are all clean no-ops.
	if deleted, err := r.store.DeleteSubscriptionByOwner(ctx, r.owner, follower); err != nil || deleted {
		t.Fatalf("redelivery: deleted=%v err=%v", deleted, err)
	}
	if deleted, err := r.store.DeleteSubscriptionByOwner(ctx, uuid.New(), follower); err != nil || deleted {
		t.Fatalf("no channel: deleted=%v err=%v", deleted, err)
	}
	if got := r.outboxTypes(t); len(got) != 2 {
		t.Fatalf("no-op deletes wrote events: %v", got)
	}
}
