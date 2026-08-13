//go:build integration

package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/atpost/group-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateGroupIdempotencySurvivesProcessRestart(t *testing.T) {
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GROUP_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS handle TEXT;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_mode TEXT NOT NULL DEFAULT 'open';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS who_can_post TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS who_can_invite TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS location TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS pending_request_count INT NOT NULL DEFAULT 0;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS group_type TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS max_members INT NOT NULL DEFAULT 0;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_questions JSONB DEFAULT '[]';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS topic_tags TEXT[] NOT NULL DEFAULT '{}';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS comment_permission TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS member_list_visible BOOLEAN NOT NULL DEFAULT true;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS link_sharing BOOLEAN NOT NULL DEFAULT true;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS is_mature BOOLEAN NOT NULL DEFAULT false;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
	`); err != nil {
		t.Fatal(err)
	}
	creatorID := uuid.New()
	group := &Group{
		Name: "Durable Space", Description: "idempotent group",
		CreatorID: creatorID, Visibility: "public", Handle: "durable-space-" + creatorID.String()[:8],
		PrivacyLevel: "public", JoinMode: "open", WhoCanPost: "all_members",
		WhoCanInvite: "all_members", Status: "active", GroupType: "public",
		JoinQuestions: json.RawMessage(`[]`), TopicTags: []string{},
		CommentPermission: "all_members", MemberListVisible: true, LinkSharing: true,
	}
	existing, err := New(pool).CreateGroup(ctx, group, "create-1")
	if err != nil || existing != nil {
		t.Fatalf("first create: existing=%v err=%v", existing, err)
	}
	firstID := group.ID

	// New Store simulates a process restart; no Redis state participates.
	retry := &Group{CreatorID: creatorID}
	existing, err = New(pool).CreateGroup(ctx, retry, "create-1")
	if err != nil || existing == nil || *existing != firstID {
		t.Fatalf("retry did not recover first group: first=%s existing=%v err=%v", firstID, existing, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM groups WHERE creator_id=$1`, creatorID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idempotent retry created %d groups", count)
	}
}
