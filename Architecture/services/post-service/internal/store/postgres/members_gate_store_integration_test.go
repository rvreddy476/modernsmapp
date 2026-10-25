//go:build integration

// Members-only from birth on the real schema (2026-10-23): the create writes
// tier_required_id with the row and its PostCreated in one transaction; the
// by-author members_only page holds only gated posts and pages; the edit's
// gate change bumps search and carries the gate; a reel draft keeps its tier
// (migration 071).
//
//	MEMBERS_POSTGRES_DSN=postgres://…/members_it_test go test -tags integration ./internal/store/postgres/ -run MembersGateIT -v
//
// The database name must end in _test (openMembersDB refuses anything else).

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func TestMembersGateITCreateWritesTheGateWithTheRow(t *testing.T) {
	pool := openMembersDB(t)
	ctx := context.Background()
	s := store.New(pool)
	author, tier := uuid.New(), uuid.New()
	p := &store.Post{
		ID: uuid.New(), AuthorID: author, Text: "the paid body", Visibility: "public", ContentType: "post",
		PostType: "text", AppOrigin: "postbook", Language: "en", License: "standard", RemixSetting: "allow",
		CommentModeration: "none", CommentAccess: "everyone", ReviewStatus: "approved", OriginalAudioVol: 1, OverlayAudioVol: 1,
		AllowDownload: true, CreatedAt: time.Now(), TierRequiredID: &tier,
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, p.ID)
		_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, p.ID)
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, p.ID)
	})
	payload := events.PostCreatedPayload{PostID: p.ID.String(), AuthorID: author.String(), TierRequiredID: tier.String(), Visibility: "public"}
	if err := s.CreatePostWithEventIdempotent(ctx, p, events.PostCreated, payload, nil); err != nil {
		t.Fatal(err)
	}
	var stored *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT tier_required_id FROM posts WHERE id = $1`, p.ID).Scan(&stored); err != nil || stored == nil || *stored != tier {
		t.Fatalf("stored tier %v err %v", stored, err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM post_outbox_events WHERE event_type = $1 AND aggregate_id = $2`, events.PostCreated, p.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got events.PostCreatedPayload
	if err := json.Unmarshal(raw, &got); err != nil || got.TierRequiredID != tier.String() || got.Text != "" {
		t.Fatalf("outbox PostCreated %s err %v", raw, err)
	}
}

func TestMembersGateITByAuthorMembersOnlyPages(t *testing.T) {
	pool := openMembersDB(t)
	ctx := context.Background()
	s := store.New(pool)
	author, tier := uuid.New(), uuid.New()
	base := time.Now().Add(-time.Hour)
	var gated []uuid.UUID
	for i := 0; i < 5; i++ {
		id := uuid.New()
		var gate *uuid.UUID
		if i%2 == 0 { // posts 0, 2, 4 gated
			gate = &tier
			gated = append(gated, id)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at, tier_required_id)
			VALUES ($1, $2, 'body', 'public', 'post', 'approved', $3, $3, $4)`, id, author, base.Add(time.Duration(i)*time.Minute), gate); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM post_engagement_counts WHERE post_id = $1`, id)
			_, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, id)
		})
	}
	var seen []uuid.UUID
	cursor := ""
	for page := 0; page < 5; page++ {
		rows, next, err := s.GetPostsByAuthorQuery(ctx, store.AuthorPostsQuery{AuthorID: author, Limit: 2, Cursor: cursor, MembersOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.TierRequiredID == nil {
				t.Fatalf("an open post on the members-only list: %s", r.ID)
			}
			seen = append(seen, r.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	// Newest first: 4, 2, 0.
	if len(seen) != 3 || seen[0] != gated[2] || seen[1] != gated[1] || seen[2] != gated[0] {
		t.Fatalf("members-only pages = %v, want %v newest first", seen, gated)
	}
	all, _, err := s.GetPostsByAuthorQuery(ctx, store.AuthorPostsQuery{AuthorID: author, Limit: 50})
	if err != nil || len(all) != 5 {
		t.Fatalf("whole grid = %d rows, err %v", len(all), err)
	}
}

func TestMembersGateITEditSetsTheGateAndTellsSearch(t *testing.T) {
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
		_, _ = pool.Exec(bg, `DELETE FROM post_edit_audit WHERE post_id = $1`, postID)
		_, _ = pool.Exec(bg, `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, postID)
		_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, postID)
	})
	lastEligibility := func() events.PostSearchEligibilityChangedPayload {
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
	after, err := s.UpdatePostFields(ctx, postID, author, store.PostEditPatch{TierRequiredID: &tier})
	if err != nil {
		t.Fatal(err)
	}
	if after.TierRequiredID == nil || *after.TierRequiredID != tier {
		t.Fatalf("edit did not set the gate: %v", after.TierRequiredID)
	}
	if ev := lastEligibility(); ev.TierRequiredID != tier.String() || ev.Text != "" {
		t.Fatalf("gated eligibility event %+v", ev)
	}
	after, err = s.UpdatePostFields(ctx, postID, author, store.PostEditPatch{ClearTierRequired: true})
	if err != nil || after.TierRequiredID != nil {
		t.Fatalf("clear: %v err %v", after.TierRequiredID, err)
	}
	if ev := lastEligibility(); ev.TierRequiredID != "" || ev.Text != "the paid body" {
		t.Fatalf("cleared eligibility event %+v", ev)
	}
}

func TestMembersGateITReelDraftKeepsItsTier(t *testing.T) {
	pool := openMembersDB(t)
	ctx := context.Background()
	s := store.New(pool)
	author, tier := uuid.New(), uuid.New()
	d := &store.ReelDraft{AuthorID: author, Visibility: "public", License: "standard", RemixSetting: "allow",
		CommentModeration: "basic", CommentAccess: "everyone", Language: "en", OriginalAudioVol: 1, OverlayAudioVol: 1}
	if err := s.CreateDraft(ctx, d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reel_drafts WHERE id = $1`, d.ID) })
	if err := s.UpdateDraft(ctx, d.ID, author, map[string]interface{}{"tier_required_id": tier}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDraft(ctx, d.ID)
	if err != nil || got.TierRequiredID == nil || *got.TierRequiredID != tier {
		t.Fatalf("draft tier %v err %v", got, err)
	}
	if err := s.UpdateDraft(ctx, d.ID, author, map[string]interface{}{"tier_required_id": nil}); err != nil {
		t.Fatal(err)
	}
	if got, err = s.GetDraft(ctx, d.ID); err != nil || got.TierRequiredID != nil {
		t.Fatalf("cleared draft tier %v err %v", got.TierRequiredID, err)
	}
}
