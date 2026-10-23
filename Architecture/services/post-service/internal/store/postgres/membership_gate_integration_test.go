//go:build integration

// PUT /v1/posts/:postId/membership on the real schema (2026-10-23): setting
// and clearing a post's members-only gate bumps search_rev and writes
// PostSearchEligibilityChanged with tier_required_id in the same
// transaction; the gated event carries no body.
//
//	MEMBERS_POSTGRES_DSN=postgres://…/members_it_test go test -tags integration ./internal/store/postgres/ -run MembershipGateOnTheRealSchema -v
//
// The database name must end in _test.

package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/atpost/post-service/database"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openMembersDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("MEMBERS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MEMBERS_POSTGRES_DSN is required")
	}
	name := dsn[strings.LastIndex(dsn, "/")+1:]
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against a database whose name does not end in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL, processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending', duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_text TEXT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN NOT NULL DEFAULT FALSE`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap real schema: %v", err)
	}
	return pool
}

func TestMembershipGateOnTheRealSchema(t *testing.T) {
	pool := openMembersDB(t)
	ctx := context.Background()
	s := store.New(pool)
	postID, author, tier := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at)
		VALUES ($1, $2, 'the paid body', 'public', 'post', 'approved', NOW(), NOW())`, postID, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, postID)
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, postID)
	})

	rev := func() int64 {
		t.Helper()
		var r int64
		if err := pool.QueryRow(ctx, `SELECT search_rev FROM posts WHERE id = $1`, postID).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	lastEvent := func() events.PostSearchEligibilityChangedPayload {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT payload FROM post_outbox_events WHERE event_type = $1 AND aggregate_id = $2 ORDER BY (payload->>'search_rev')::bigint DESC LIMIT 1`,
			events.PostSearchEligibilityChanged, postID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var p events.PostSearchEligibilityChangedPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	before := rev()
	if err := s.SetPostMembershipGate(ctx, postID, &tier); err != nil {
		t.Fatal(err)
	}
	if got := rev(); got != before+1 {
		t.Fatalf("search_rev %d -> %d; want one bump", before, got)
	}
	gated := lastEvent()
	if gated.TierRequiredID != tier.String() || gated.SearchRev != before+1 || gated.Text != "" {
		t.Fatalf("gated event %+v", gated)
	}
	var stored *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT tier_required_id FROM posts WHERE id = $1`, postID).Scan(&stored); err != nil || stored == nil || *stored != tier {
		t.Fatalf("stored tier %v err %v", stored, err)
	}

	if err := s.SetPostMembershipGate(ctx, postID, nil); err != nil {
		t.Fatal(err)
	}
	cleared := lastEvent()
	if cleared.TierRequiredID != "" || cleared.SearchRev != before+2 || cleared.Text != "the paid body" {
		t.Fatalf("cleared event %+v", cleared)
	}

	if err := s.SetPostMembershipGate(ctx, uuid.New(), &tier); err == nil || err.Error() != "POST_NOT_FOUND" {
		t.Fatalf("missing post: %v", err)
	}
}
