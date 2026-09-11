//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"time"

	"github.com/atpost/analytics-service/database"
	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The two data migrations in this stream are SQL nobody can unit-test:
// 009 backfills one session per historical play_end and 010 collapses
// the double-counted likes. testsupport already applied them (to an
// empty database) when it bootstrapped; these run their text again over
// fixture rows, which both are written to tolerate, and check what they
// leave behind.

func migrationSQL(t *testing.T, name string) string {
	t.Helper()
	body, err := fs.ReadFile(database.Migrations, "migrations/"+name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func migrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	testsupport.RequireTestDSN(t)
	pool := testsupport.Pool(t, "pgstore")
	if _, err := pool.Exec(context.Background(), `TRUNCATE analytics.ingest_receipts, analytics.events_raw,
		analytics.content_ownership, analytics.playback_sessions CASCADE`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func insertRaw(t *testing.T, pool *pgxpool.Pool, id, user, session uuid.UUID, eventType string, ts time.Time, payload map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	var sess *uuid.UUID
	if session != uuid.Nil {
		sess = &session
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.events_raw (id, user_id, session_id, type, payload, ts, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`, id, user, sess, eventType, raw, ts); err != nil {
		t.Fatal(err)
	}
}

func TestBackfillMigrationWritesOneSessionPerPlayEnd(t *testing.T) {
	ctx := context.Background()
	pool := migrationPool(t)
	store := postgres.New(pool)
	creator, viewer, content := uuid.New(), uuid.New(), uuid.New()
	if err := store.UpsertContentOwnership(ctx, postgres.ContentOwnership{ContentID: content, CreatorID: creator, ContentType: "flick", CreatedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour).Add(-26 * time.Hour)
	playEnd := func(user uuid.UUID, watched int64, pct float64, display bool) map[string]any {
		return map[string]any{
			"content_id": content.String(), "creator_id": creator.String(), "content_type": "flick",
			"content_duration_ms": 20_000, "watched_ms_total": watched, "percent_viewed": pct,
			"loop_count": 0, "end_reason": "ended", "is_display_view": display, "session_id": uuid.New().String(),
		}
	}
	strangerSession, creatorSession, liveSession := uuid.New(), uuid.New(), uuid.New()
	// A stranger's display view, the creator's own, and one that is not a
	// display view. Plus a duplicate play_end for the stranger's session
	// (pre-006 data had no unique index), later by a minute.
	insertRaw(t, pool, uuid.New(), viewer, strangerSession, "play_end", hour.Add(5*time.Minute), playEnd(viewer, 15_000, 75, true))
	insertRaw(t, pool, uuid.New(), viewer, strangerSession, "play_end", hour.Add(6*time.Minute), playEnd(viewer, 19_000, 95, true))
	insertRaw(t, pool, uuid.New(), creator, creatorSession, "play_end", hour.Add(7*time.Minute), playEnd(creator, 20_000, 100, true))
	insertRaw(t, pool, uuid.New(), uuid.New(), uuid.New(), "play_end", hour.Add(8*time.Minute), playEnd(viewer, 1_000, 5, false))
	// Not sessions: a heartbeat, and a play_end with no session id.
	insertRaw(t, pool, uuid.New(), viewer, uuid.New(), "watch_heartbeat", hour.Add(9*time.Minute), map[string]any{"content_id": content.String(), "creator_id": creator.String()})
	insertRaw(t, pool, uuid.New(), viewer, uuid.Nil, "play_end", hour.Add(10*time.Minute), playEnd(viewer, 15_000, 75, true))
	// A session already written live since 008 shipped must win over
	// its backfilled shadow.
	if _, err := pool.Exec(ctx, `INSERT INTO analytics.playback_sessions
		(actor_id, session_id, content_id, creator_id, content_type, first_seen, last_seen, watched_ms, source)
		VALUES ($1, $2, $3, $4, 'flick', $5, $5, 12345, 'live')`, viewer, liveSession, content, creator, hour); err != nil {
		t.Fatal(err)
	}
	insertRaw(t, pool, uuid.New(), viewer, liveSession, "play_end", hour.Add(11*time.Minute), playEnd(viewer, 15_000, 75, true))

	if _, err := pool.Exec(ctx, migrationSQL(t, "009_backfill_sessions_from_play_end.sql")); err != nil {
		t.Fatalf("009: %v", err)
	}

	var total, backfilled int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE source = 'backfill') FROM analytics.playback_sessions`).Scan(&total, &backfilled); err != nil {
		t.Fatal(err)
	}
	if total != 4 || backfilled != 3 {
		t.Fatalf("sessions=%d backfilled=%d, want 4 and 3 (stranger, creator, non-view; live row kept)", total, backfilled)
	}

	stranger, err := store.GetPlaybackSession(ctx, viewer, strangerSession, content)
	if err != nil {
		t.Fatal(err)
	}
	// The earliest play_end for the session is the one kept.
	if stranger.WatchedMS != 15_000 || stranger.PercentViewed != 75 || stranger.PercentCovered != 75 {
		t.Fatalf("stranger: watched=%d viewed=%v covered=%v", stranger.WatchedMS, stranger.PercentViewed, stranger.PercentCovered)
	}
	if !stranger.FirstSeen.Equal(hour.Add(5*time.Minute)) || stranger.FinalizedAt == nil || *stranger.FinalizeReason != postgres.FinalizePlayEnd {
		t.Fatalf("stranger attribution: first_seen=%s finalized=%v reason=%v", stranger.FirstSeen, stranger.FinalizedAt, stranger.FinalizeReason)
	}
	if !stranger.IsDisplayView || stranger.ViewScore != 0.75 || stranger.IsSelfView || stranger.CoveredMS != 15_000 || stranger.Source != "backfill" {
		t.Fatalf("stranger flags: %+v", stranger)
	}
	own, err := store.GetPlaybackSession(ctx, creator, creatorSession, content)
	if err != nil {
		t.Fatal(err)
	}
	if !own.IsSelfView || !own.IsDisplayView {
		t.Fatalf("creator's own: self=%v display=%v (display stays honest; exclusion is the aggregator's)", own.IsSelfView, own.IsDisplayView)
	}
	live, err := store.GetPlaybackSession(ctx, viewer, liveSession, content)
	if err != nil {
		t.Fatal(err)
	}
	if live.Source != "live" || live.WatchedMS != 12345 {
		t.Fatalf("live row was overwritten by the backfill: %+v", live)
	}

	// Idempotent: a second run changes nothing.
	if _, err := pool.Exec(ctx, migrationSQL(t, "009_backfill_sessions_from_play_end.sql")); err != nil {
		t.Fatalf("009 rerun: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.playback_sessions`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Fatalf("rerun sessions=%d want 4", total)
	}
}

func TestLikeRepairMigrationKeepsOneLikePerViewerAndContent(t *testing.T) {
	ctx := context.Background()
	pool := migrationPool(t)
	store := postgres.New(pool)
	creator, viewer, content, unprojected := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := store.UpsertContentOwnership(ctx, postgres.ContentOwnership{ContentID: content, CreatorID: creator, ContentType: "flick", CreatedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Add(-3 * time.Hour)
	like := func(c uuid.UUID) map[string]any {
		return map[string]any{"content_id": c.String(), "creator_id": creator.String()}
	}
	// The HTTP copy: a receipt in the old per-session shape, then the row.
	httpID, httpSession := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO analytics.ingest_receipts (event_id, actor_id, session_id, content_id, event_type, dedupe_key)
		VALUES ('evt-old-http-like-000001', $1, $2, $3, 'like', 'session')`, viewer, httpSession, content); err != nil {
		t.Fatal(err)
	}
	insertRaw(t, pool, httpID, viewer, httpSession, "like", when, like(content))
	// The Kafka copy, a minute later, no receipt, no session — twice,
	// because the consumer was redelivered.
	insertRaw(t, pool, uuid.New(), viewer, uuid.Nil, "like", when.Add(time.Minute), like(content))
	insertRaw(t, pool, uuid.New(), viewer, uuid.Nil, "like", when.Add(2*time.Minute), like(content))
	// Another viewer's single like, and a like on content never projected.
	other := uuid.New()
	insertRaw(t, pool, uuid.New(), other, uuid.Nil, "like", when, like(content))
	insertRaw(t, pool, uuid.New(), viewer, uuid.Nil, "like", when, like(unprojected))
	// A comment must be untouched.
	insertRaw(t, pool, uuid.New(), viewer, uuid.Nil, "comment_create", when, like(content))

	if _, err := pool.Exec(ctx, migrationSQL(t, "010_engagement_like_repair.sql")); err != nil {
		t.Fatalf("010: %v", err)
	}

	var viewerLikes, otherLikes, unprojectedLikes, comments int
	var survivor uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT count(*), min(id::text)::uuid FROM analytics.events_raw WHERE type='like' AND user_id=$1 AND payload->>'content_id'=$2`, viewer, content.String()).Scan(&viewerLikes, &survivor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.events_raw WHERE type='like' AND user_id=$1`, other).Scan(&otherLikes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.events_raw WHERE type='like' AND payload->>'content_id'=$1`, unprojected.String()).Scan(&unprojectedLikes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.events_raw WHERE type='comment_create'`).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if viewerLikes != 1 || survivor != httpID {
		t.Fatalf("viewer likes=%d survivor=%s, want 1 and the earliest (%s)", viewerLikes, survivor, httpID)
	}
	if otherLikes != 1 || unprojectedLikes != 1 || comments != 1 {
		t.Fatalf("collateral: other=%d unprojected=%d comments=%d, want 1 each", otherLikes, unprojectedLikes, comments)
	}

	// Receipts: one per surviving like on projected content, in the new
	// shape, so a future Kafka copy of the same like is a duplicate.
	type receipt struct {
		eventID string
		session uuid.UUID
		key     *string
	}
	rows, err := pool.Query(ctx, `SELECT event_id, session_id, dedupe_key FROM analytics.ingest_receipts WHERE event_type='like' ORDER BY actor_id`)
	if err != nil {
		t.Fatal(err)
	}
	var receipts []receipt
	for rows.Next() {
		var r receipt
		if err := rows.Scan(&r.eventID, &r.session, &r.key); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, r)
	}
	rows.Close()
	if len(receipts) != 2 {
		t.Fatalf("like receipts=%d want 2 (viewer, other); unprojected content cannot carry one", len(receipts))
	}
	for _, r := range receipts {
		if r.session != uuid.Nil || r.key == nil || *r.key != postgres.LikeDedupeKey || len(r.eventID) < 16 {
			t.Fatalf("receipt in the old shape survived: %+v", r)
		}
	}
	// And the collapse now holds at the database: a Kafka redelivery
	// keyed on the outbox id is a duplicate of the repaired receipt.
	key := postgres.LikeDedupeKey
	inserted, err := store.InsertAcceptedBatch(ctx, []postgres.Event{{
		ID: uuid.New(), ClientEventID: "kafka:" + uuid.New().String(), UserID: viewer, SessionID: uuid.Nil,
		ContentID: content, Type: "like", DedupeKey: &key, Payload: []byte(`{}`), Timestamp: when, ReceivedAt: when,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inserted) != 0 {
		t.Fatal("a Kafka like after the repair was written as a second row")
	}
}
