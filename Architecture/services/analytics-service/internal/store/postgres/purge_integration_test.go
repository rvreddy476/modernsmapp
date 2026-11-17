//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reels-8 against a real database (CI only, a *_test database through
// testsupport.Pool): PurgeUser removes every PurgeDelete row of the
// purged user, in batches past the batch size, twice without error, and
// leaves other users' rows and the retained creator-side rows alone.

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}

func countWhere(t *testing.T, pool *pgxpool.Pool, table, column string, id uuid.UUID) int {
	t.Helper()
	var n int
	q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, table, column)
	if err := pool.QueryRow(context.Background(), q, id).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func seedPurgeUser(t *testing.T, pool *pgxpool.Pool, viewer, content, creator uuid.UUID, session uuid.UUID, heartbeats int) {
	t.Helper()
	now := time.Now().UTC()
	payload := fmt.Sprintf(`{"content_id":"%s","creator_id":"%s"}`, content, creator)
	mustExec(t, pool, `
		INSERT INTO analytics.events_raw (id, user_id, session_id, type, payload, ts, received_at)
		VALUES (gen_random_uuid(), $1, $2, 'play_end', $3::jsonb, $4, $4)`, viewer, session, payload, now)
	mustExec(t, pool, `
		INSERT INTO analytics.events_raw (id, user_id, session_id, type, payload, ts, received_at)
		SELECT gen_random_uuid(), $1, $2, 'watch_heartbeat', $3::jsonb, $4, $4 FROM generate_series(1, $5)`,
		viewer, session, payload, now, heartbeats)
	mustExec(t, pool, `
		INSERT INTO analytics.ingest_receipts (event_id, actor_id, session_id, content_id, event_type)
		SELECT 'purge-it-' || $5::text || '-' || lpad(i::text, 8, '0'), $1, $2, $3, 'watch_heartbeat'
		FROM generate_series(1, $4) AS i`, viewer, session, content, heartbeats, viewer.String())
	mustExec(t, pool, `
		INSERT INTO analytics.playback_sessions
			(actor_id, session_id, content_id, creator_id, content_type, first_seen, last_seen)
		VALUES ($1, $2, $3, $4, 'flick', $5, $5)`, viewer, session, content, creator, now)
	mustExec(t, pool, `
		INSERT INTO analytics.creator_follower_daily (creator_id, day_bucket, follows)
		VALUES ($1, CURRENT_DATE, 1) ON CONFLICT DO NOTHING`, viewer)
	mustExec(t, pool, `INSERT INTO user_streaks (user_id, type) VALUES ($1, 'daily_login') ON CONFLICT DO NOTHING`, viewer)
	mustExec(t, pool, `INSERT INTO user_badges (user_id, type) VALUES ($1, 'creator') ON CONFLICT DO NOTHING`, viewer)
	mustExec(t, pool, `INSERT INTO loyalty_points (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, viewer)
	mustExec(t, pool, `INSERT INTO point_transactions (user_id, amount, type) VALUES ($1, 5, 'referral')`, viewer)
	mustExec(t, pool, `
		INSERT INTO mission_progress (mission_id, user_id)
		SELECT id, $1 FROM missions WHERE title = 'purge-it' ON CONFLICT DO NOTHING`, viewer)
}

func TestPurgeUserErasesEveryDeleteTargetAndOnlyThat(t *testing.T) {
	testsupport.RequireTestDSN(t)
	pool := testsupport.Pool(t, "pgstore")
	ctx := context.Background()
	store := postgres.New(pool)

	gone, kept, otherCreator := uuid.New(), uuid.New(), uuid.New()
	theirContent, goneContent := uuid.New(), uuid.New()
	goneSession, keptSession := uuid.New(), uuid.New()

	mustExec(t, pool, `
		INSERT INTO missions (title, description, type, target, reward_type, starts_at, ends_at)
		VALUES ('purge-it', 'x', 'post_count', 1, 'points', now(), now() + interval '1 day')`)
	for content, creator := range map[uuid.UUID]uuid.UUID{theirContent: otherCreator, goneContent: gone} {
		mustExec(t, pool, `
			INSERT INTO analytics.content_ownership (content_id, creator_id, content_type, created_at)
			VALUES ($1, $2, 'flick', now())`, content, creator)
	}
	mustExec(t, pool, `
		INSERT INTO analytics.content_daily_summary (content_id, day_bucket, creator_id, content_type, views_display)
		VALUES ($1, CURRENT_DATE, $2, 'flick', 7)`, goneContent, gone)

	// The purged user watched someone else's reel, past one delete batch.
	seedPurgeUser(t, pool, gone, theirContent, otherCreator, goneSession, 5003)
	// Another viewer watched the purged user's reel.
	seedPurgeUser(t, pool, kept, goneContent, gone, keptSession, 3)

	keys, err := store.WatchSessionKeys(ctx, gone, model.WatchSessionKey{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].ContentID != theirContent || keys[0].SessionID != goneSession.String() {
		t.Fatalf("watch session keys = %+v, want the one (content, session) pair", keys)
	}

	for i := 0; i < 2; i++ { // idempotent
		if err := store.PurgeUser(ctx, gone); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	for _, p := range postgres.PurgePlan {
		if p.Action != postgres.PurgeDelete {
			continue
		}
		if n := countWhere(t, pool, p.Table, p.Column, gone); n != 0 {
			t.Errorf("%s.%s still holds %d rows of the purged user", p.Table, p.Column, n)
		}
		if n := countWhere(t, pool, p.Table, p.Column, kept); n == 0 {
			t.Errorf("%s.%s lost the other user's rows", p.Table, p.Column)
		}
	}
	if keys, err := store.WatchSessionKeys(ctx, gone, model.WatchSessionKey{}, 100); err != nil || len(keys) != 0 {
		t.Fatalf("keys after purge = %v, %v", keys, err)
	}
	// Retained creator side: ownership, the daily summary and the other
	// viewer's session on the purged creator's reel.
	for _, c := range []struct{ table, column string }{
		{"analytics.content_ownership", "creator_id"},
		{"analytics.content_daily_summary", "creator_id"},
		{"analytics.playback_sessions", "creator_id"},
	} {
		if n := countWhere(t, pool, c.table, c.column, gone); n == 0 {
			t.Errorf("retained %s.%s was deleted", c.table, c.column)
		}
	}
}
