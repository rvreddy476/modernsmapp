//go:build integration

package postgres_test

// Integration coverage for PurgeUser (internal/store/postgres/purge.go),
// following the same M7_POSTGRES_DSN / openM7PostDB pattern as
// moderation_authority_integration_test.go in this package: a real Postgres
// database, database.SetupSQL applied verbatim (posts, comments,
// post_engagement_counts, polls/poll_options/poll_votes, post_media,
// post_reposts, post_drafts, post_hidden_authors, ...), the real
// migrations/033_post_moderation_authority.sql (openM7PostDB applies it
// unconditionally), plus the handful of tables that only exist via other
// migrations/ files or cmd/server/main.go's ensureSchema (reactions,
// saved_items, reel_hashtags, reel_crosspost, slug_history,
// moderation_reviews, post_product_tags, watch_progress, content_reports,
// comment_idempotency, reel_drafts, video_series) created here with the
// same column names PurgeUser's SQL references. FK constraints to the
// shared app DB's users/channels tables are intentionally not replicated —
// PurgeUser's own statements are what is
// under test, not those cross-service references.

import (
	"context"
	"testing"

	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func purgeTestExtraSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS reactions (
			id            UUID PRIMARY KEY,
			target_type   TEXT NOT NULL,
			target_id     UUID NOT NULL,
			user_id       UUID NOT NULL,
			reaction_type TEXT NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS comment_idempotency (
			actor_id    UUID NOT NULL,
			post_id     UUID NOT NULL,
			client_key  TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			comment_id  UUID NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (actor_id, post_id, client_key)
		)`,
		`CREATE TABLE IF NOT EXISTS post_product_tags (
			id                UUID PRIMARY KEY,
			post_id           UUID NOT NULL,
			affiliate_link_id UUID NOT NULL,
			creator_id        UUID NOT NULL,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS reel_hashtags (
			reel_id    UUID NOT NULL,
			hashtag    TEXT NOT NULL,
			position   INT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (reel_id, hashtag)
		)`,
		`CREATE TABLE IF NOT EXISTS reel_crosspost (
			id             UUID PRIMARY KEY,
			source_reel_id UUID NOT NULL,
			target_type    TEXT NOT NULL,
			target_id      TEXT,
			status         TEXT NOT NULL DEFAULT 'pending',
			created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS slug_history (
			id         UUID PRIMARY KEY,
			reel_id    UUID NOT NULL,
			old_slug   TEXT NOT NULL,
			new_slug   TEXT NOT NULL,
			changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS moderation_reviews (
			id            UUID PRIMARY KEY,
			reel_id       UUID NOT NULL,
			reviewer_type TEXT NOT NULL,
			decision      TEXT NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS watch_progress (
			user_id         UUID NOT NULL,
			post_id         UUID NOT NULL,
			position_ms     INT NOT NULL DEFAULT 0,
			duration_ms     INT NOT NULL DEFAULT 0,
			percent_watched REAL NOT NULL DEFAULT 0,
			completed       BOOLEAN NOT NULL DEFAULT FALSE,
			last_watched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (user_id, post_id)
		)`,
		`CREATE TABLE IF NOT EXISTS saved_items (
			id              UUID PRIMARY KEY,
			user_id         UUID NOT NULL,
			target_type     TEXT NOT NULL,
			target_id       UUID NOT NULL,
			collection_name TEXT NOT NULL DEFAULT 'default',
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS content_reports (
			id          UUID PRIMARY KEY,
			reporter_id UUID NOT NULL,
			target_type TEXT NOT NULL,
			target_id   UUID NOT NULL,
			reason      TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'pending',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS reel_drafts (
			id         UUID PRIMARY KEY,
			author_id  UUID NOT NULL,
			title      TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL DEFAULT 'draft',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		// post_moderation_decisions is NOT created here: openM7PostDB (in
		// moderation_authority_integration_test.go, same package) already
		// applies the real migrations/033_post_moderation_authority.sql,
		// and this test's insert below matches that real shape.
		`CREATE TABLE IF NOT EXISTS video_series (
			id              UUID PRIMARY KEY,
			creator_id      UUID NOT NULL,
			trailer_post_id UUID REFERENCES posts(id),
			title           TEXT NOT NULL DEFAULT '',
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	}
	for _, stmt := range ddl {
		if _, err := pool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("purge test schema: %v\nstatement: %s", err, stmt)
		}
	}
}

func insertPost(t *testing.T, pool *pgxpool.Pool, id, authorID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at)
		VALUES ($1, $2, 'purge proof', 'public', 'post', NOW(), NOW())`,
		id, authorID)
	if err != nil {
		t.Fatalf("insert post: %v", err)
	}
}

func TestPurgeUserIsIdempotentAndDecrementsSurvivorCounters(t *testing.T) {
	pool := openM7PostDB(t)
	purgeTestExtraSchema(t, pool)
	ctx := context.Background()
	s := store.New(pool)

	userA, userB := uuid.New(), uuid.New()
	postA1 := uuid.New() // userA's own post — purged entirely
	postB1 := uuid.New() // userB's post — survives, counters decrement

	insertPost(t, pool, postA1, userA)
	insertPost(t, pool, postB1, userB)

	// userA's own post carries dependent rows with no CASCADE from posts:
	// post_media, a poll (+option +vote), reel/product metadata, a report
	// against it, and a moderation decision (ON DELETE RESTRICT).
	mediaID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status) VALUES ($1,$2,'image','ready')`, mediaID, userA); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO post_media (post_id, media_id, kind) VALUES ($1,$2,'image')`, postA1, mediaID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO polls (post_id, question) VALUES ($1,'q?')`, postA1); err != nil {
		t.Fatal(err)
	}
	optionID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO poll_options (id, post_id, label) VALUES ($1,$2,'opt')`, optionID, postA1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO poll_votes (post_id, option_id, user_id) VALUES ($1,$2,$3)`, postA1, optionID, userB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reel_hashtags (reel_id, hashtag) VALUES ($1,'proof')`, postA1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reel_crosspost (id, source_reel_id, target_type) VALUES ($1,$2,'feed')`, uuid.New(), postA1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO slug_history (id, reel_id, old_slug, new_slug) VALUES ($1,$2,'old','new')`, uuid.New(), postA1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO moderation_reviews (id, reel_id, reviewer_type, decision) VALUES ($1,$2,'auto','approved')`, uuid.New(), postA1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO post_product_tags (id, post_id, affiliate_link_id, creator_id) VALUES ($1,$2,$3,$4)`, uuid.New(), postA1, uuid.New(), userA); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO content_reports (id, reporter_id, target_type, target_id, reason) VALUES ($1,$2,'post',$3,'spam')`, uuid.New(), userB, postA1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO post_moderation_decisions
			(decision_id, post_id, actor_id, action, reason, source, previous_status, resulting_status, changed)
		VALUES ($1,$2,$3,'approve','confirmed policy compliant','admin','pending','approved',true)`,
		uuid.New(), postA1, uuid.New()); err != nil {
		t.Fatal(err)
	}
	// Another creator's video_series has a dangling trailer reference into
	// userA's post — must be nulled, not left to fail the transaction.
	seriesID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO video_series (id, creator_id, trailer_post_id) VALUES ($1,$2,$3)`, seriesID, userB, postA1); err != nil {
		t.Fatal(err)
	}

	// userA's drafts.
	if _, err := pool.Exec(ctx, `INSERT INTO post_drafts (id, author_id, post_type, payload) VALUES ($1,$2,'post','{}'::jsonb)`, uuid.New(), userA); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reel_drafts (id, author_id) VALUES ($1,$2)`, uuid.New(), userA); err != nil {
		t.Fatal(err)
	}

	// userA authored a comment on userB's surviving post — comment_count
	// must decrement by exactly 1.
	commentID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO comments (id, post_id, author_id, body) VALUES ($1,$2,$3,'nice post')`, commentID, postB1, userA); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO comment_idempotency (actor_id, post_id, client_key, fingerprint, comment_id) VALUES ($1,$2,'k1','f1',$3)`, userA, postB1, commentID); err != nil {
		t.Fatal(err)
	}
	// A reply from userB to userA's comment must be orphaned (parent_id set
	// NULL), not left dangling or blocking the delete.
	replyID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO comments (id, post_id, author_id, parent_id, body, is_reply) VALUES ($1,$2,$3,$4,'reply',true)`, replyID, postB1, userB, commentID); err != nil {
		t.Fatal(err)
	}

	// userA reacted on userB's post — like_count decrements by 1.
	if _, err := pool.Exec(ctx, `INSERT INTO reactions (id, target_type, target_id, user_id, reaction_type) VALUES ($1,'post',$2,$3,'like')`, uuid.New(), postB1, userA); err != nil {
		t.Fatal(err)
	}
	// userA reposted userB's post — repost_count decrements by 1, and the
	// repost row itself is removed.
	repostID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO post_reposts (id, user_id, original_post_id, repost_type, status) VALUES ($1,$2,$3,'plain','active')`, repostID, userA, postB1); err != nil {
		t.Fatal(err)
	}
	// userA saved userB's post, and watched part of it.
	if _, err := pool.Exec(ctx, `INSERT INTO saved_items (id, user_id, target_type, target_id) VALUES ($1,$2,'post',$3)`, uuid.New(), userA, postB1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO watch_progress (user_id, post_id, duration_ms) VALUES ($1,$2,1000)`, userA, postB1); err != nil {
		t.Fatal(err)
	}

	// Baseline counters on the survivor: seed non-zero so the decrement is
	// unambiguous (the AFTER-INSERT trigger creates the row at all-zero).
	if _, err := pool.Exec(ctx, `UPDATE post_engagement_counts SET comment_count=5, like_count=5, repost_count=5 WHERE post_id=$1`, postB1); err != nil {
		t.Fatal(err)
	}

	// userA is hidden (deactivated) before the purge lands, same as the
	// real event ordering (deactivate/schedule always precedes purge).
	if err := s.SetUserHidden(ctx, userA, true, "user.deletion_scheduled"); err != nil {
		t.Fatalf("SetUserHidden: %v", err)
	}

	if err := s.PurgeUser(ctx, userA); err != nil {
		t.Fatalf("first PurgeUser: %v", err)
	}

	// userA's own post and everything keyed to it is gone.
	assertZero(t, pool, "posts", "id=$1", postA1)
	assertZero(t, pool, "post_media", "post_id=$1", postA1)
	assertZero(t, pool, "polls", "post_id=$1", postA1)
	assertZero(t, pool, "poll_options", "post_id=$1", postA1)
	assertZero(t, pool, "poll_votes", "post_id=$1", postA1)
	assertZero(t, pool, "reel_hashtags", "reel_id=$1", postA1)
	assertZero(t, pool, "reel_crosspost", "source_reel_id=$1", postA1)
	assertZero(t, pool, "slug_history", "reel_id=$1", postA1)
	assertZero(t, pool, "moderation_reviews", "reel_id=$1", postA1)
	assertZero(t, pool, "post_product_tags", "post_id=$1", postA1)
	assertZero(t, pool, "content_reports", "target_id=$1", postA1)
	assertZero(t, pool, "post_moderation_decisions", "post_id=$1", postA1)
	assertZero(t, pool, "post_engagement_counts", "post_id=$1", postA1)
	assertZero(t, pool, "post_drafts", "author_id=$1", userA)
	assertZero(t, pool, "reel_drafts", "author_id=$1", userA)
	assertZero(t, pool, "post_hidden_authors", "user_id=$1", userA)

	// Cross-user rows owned by userA are gone.
	assertZero(t, pool, "comments", "id=$1", commentID)
	assertZero(t, pool, "comment_idempotency", "actor_id=$1", userA)
	assertZero(t, pool, "reactions", "user_id=$1", userA)
	assertZero(t, pool, "post_reposts", "id=$1", repostID)
	assertZero(t, pool, "saved_items", "user_id=$1", userA)
	assertZero(t, pool, "watch_progress", "user_id=$1", userA)

	// The reply survives, orphaned.
	var parentID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT parent_id FROM comments WHERE id=$1`, replyID).Scan(&parentID); err != nil {
		t.Fatalf("reply must survive: %v", err)
	}
	if parentID != nil {
		t.Fatalf("reply parent_id must be nulled, got %v", *parentID)
	}

	// The dangling trailer reference is nulled, not fatal.
	var trailer *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT trailer_post_id FROM video_series WHERE id=$1`, seriesID).Scan(&trailer); err != nil {
		t.Fatalf("video_series row must survive: %v", err)
	}
	if trailer != nil {
		t.Fatalf("trailer_post_id must be nulled, got %v", *trailer)
	}

	// The survivor's counters decremented by exactly 1 each.
	var commentCount, likeCount, repostCount int
	if err := pool.QueryRow(ctx, `SELECT comment_count, like_count, repost_count FROM post_engagement_counts WHERE post_id=$1`, postB1).
		Scan(&commentCount, &likeCount, &repostCount); err != nil {
		t.Fatal(err)
	}
	if commentCount != 4 || likeCount != 4 || repostCount != 4 {
		t.Fatalf("survivor counters = (%d,%d,%d), want (4,4,4)", commentCount, likeCount, repostCount)
	}
	// The survivor's own post is untouched.
	assertOne(t, pool, "posts", "id=$1", postB1)

	// ── Idempotent redelivery ────────────────────────────────────────────
	if err := s.PurgeUser(ctx, userA); err != nil {
		t.Fatalf("second PurgeUser (redelivery) must succeed as a no-op: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT comment_count, like_count, repost_count FROM post_engagement_counts WHERE post_id=$1`, postB1).
		Scan(&commentCount, &likeCount, &repostCount); err != nil {
		t.Fatal(err)
	}
	if commentCount != 4 || likeCount != 4 || repostCount != 4 {
		t.Fatalf("second purge must not double-decrement: got (%d,%d,%d), want (4,4,4)", commentCount, likeCount, repostCount)
	}
}

func assertZero(t *testing.T, pool *pgxpool.Pool, table, where string, arg any) {
	t.Helper()
	var n int
	q := "SELECT count(*) FROM " + table + " WHERE " + where
	if err := pool.QueryRow(context.Background(), q, arg).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if n != 0 {
		t.Fatalf("%s: want 0 rows matching %s, got %d", table, where, n)
	}
}

func assertOne(t *testing.T, pool *pgxpool.Pool, table, where string, arg any) {
	t.Helper()
	var n int
	q := "SELECT count(*) FROM " + table + " WHERE " + where
	if err := pool.QueryRow(context.Background(), q, arg).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if n != 1 {
		t.Fatalf("%s: want exactly 1 row matching %s, got %d", table, where, n)
	}
}
