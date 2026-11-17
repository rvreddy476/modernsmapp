//go:build integration

// Report resolution and REG-6 on the real schema (lane post-service-2,
// 2026-11-18). Scratch database only (CI's *_test database):
//
//	M7_POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run 'ReportResolutionIT|FailedScheduledVideoIT' -v

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func authorNotices(t *testing.T, pool *pgxpool.Pool, postID uuid.UUID) []store.AuthorNoticePayload {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT payload FROM post_outbox_events
		WHERE aggregate_id = $1 AND event_type = $2 ORDER BY created_at, seq`, postID, store.EventPostAuthorNotice)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []store.AuthorNoticePayload
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var n store.AuthorNoticePayload
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func reviewStatus(t *testing.T, pool *pgxpool.Pool, postID uuid.UUID) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT review_status FROM posts WHERE id=$1`, postID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// An upheld report takes the post down (rejected: hidden from everyone but
// the author, out of search), the author is told why with the report as the
// source, and the author's appeal, overturned, brings it back and says so.
func TestReportResolutionIT_UpheldReportHidesTellsAndAppealReverses(t *testing.T) {
	pool := openM7PostDB(t)
	ctx := context.Background()
	s := store.New(pool)
	postID, authorID := seedModerationPost(t, pool, "approved")
	reportID, moderator := uuid.New(), uuid.New()

	d, err := s.ModeratePost(ctx, store.ModeratePostInput{
		DecisionID: reportID, PostID: postID, ActorID: moderator,
		Action: "reject", Reason: "Harassment: targets a named person", Source: "report", SourceRefID: &reportID,
	})
	if err != nil {
		t.Fatalf("report takedown: %v", err)
	}
	if d.Source != "report" || !d.Changed || reviewStatus(t, pool, postID) != "rejected" {
		t.Fatalf("decision %+v status %s", d, reviewStatus(t, pool, postID))
	}
	var narrowed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1 AND event_type='post.visibility_changed'`, postID).Scan(&narrowed); err != nil {
		t.Fatal(err)
	}
	if narrowed != 1 {
		t.Fatalf("visibility_changed events = %d, want 1 (every cached copy dropped)", narrowed)
	}
	notices := authorNotices(t, pool, postID)
	if len(notices) != 1 {
		t.Fatalf("notices = %+v, want one takedown", notices)
	}
	n := notices[0]
	if n.Kind != store.AuthorNoticeModerationTakedown || n.AuthorID != authorID.String() ||
		n.Reason != "Harassment: targets a named person" || n.Source != "report" ||
		n.SourceRefID != reportID.String() || !n.Appealable {
		t.Fatalf("takedown notice = %+v", n)
	}
	// The replay of the same resolution is the same decision: no second notice.
	if _, err := s.ModeratePost(ctx, store.ModeratePostInput{
		DecisionID: reportID, PostID: postID, ActorID: moderator,
		Action: "reject", Reason: "Harassment: targets a named person", Source: "report", SourceRefID: &reportID,
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := len(authorNotices(t, pool, postID)); got != 1 {
		t.Fatalf("replay wrote %d notices, want still 1", got)
	}

	appealID := uuid.New()
	if _, err := s.ModeratePost(ctx, store.ModeratePostInput{
		DecisionID: appealID, PostID: postID, ActorID: uuid.New(),
		Action: "approve", Reason: "Appeal upheld: satire", Source: "appeal", SourceRefID: &appealID,
	}); err != nil {
		t.Fatalf("appeal overturn: %v", err)
	}
	if st := reviewStatus(t, pool, postID); st != "approved" {
		t.Fatalf("after appeal status %s, want approved", st)
	}
	notices = authorNotices(t, pool, postID)
	if len(notices) != 2 || notices[1].Kind != store.AuthorNoticeModerationRestored || notices[1].Source != "appeal" {
		t.Fatalf("notices after appeal = %+v", notices)
	}
}

// A report can only take a post down: a report-sourced approve is refused
// and nothing is written.
func TestReportResolutionIT_ReportCannotApprove(t *testing.T) {
	pool := openM7PostDB(t)
	ctx := context.Background()
	s := store.New(pool)
	postID, _ := seedModerationPost(t, pool, "rejected")
	id := uuid.New()
	_, err := s.ModeratePost(ctx, store.ModeratePostInput{
		DecisionID: id, PostID: postID, ActorID: uuid.New(),
		Action: "approve", Reason: "report dismissed", Source: "report", SourceRefID: &id,
	})
	if !errors.Is(err, store.ErrModerationInput) {
		t.Fatalf("report approve: %v, want ErrModerationInput", err)
	}
	var decisions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM post_moderation_decisions WHERE decision_id=$1`, id).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions != 0 || reviewStatus(t, pool, postID) != "rejected" || len(authorNotices(t, pool, postID)) != 0 {
		t.Fatal("a refused report decision wrote something")
	}
}

// REG-6: a scheduled video whose media failed leaves the schedule into the
// rejected state and its creator is told once; a healthy scheduled video is
// left alone.
func TestFailedScheduledVideoIT_LeavesTheScheduleRejectedAndNotified(t *testing.T) {
	pool := openM7PostDB(t)
	ctx := context.Background()
	s := store.New(pool)
	author := uuid.New()
	failed, healthy := uuid.New(), uuid.New()
	for id, status := range map[uuid.UUID]string{failed: "failed", healthy: "processing"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,search_rev,created_at,updated_at,publish_at)
			VALUES ($1,$2,'REG-6 proof','public','long_video','pending',1,NOW(),NOW(),NOW() + interval '1 day')`, id, author); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO video_metadata (post_id, upload_status) VALUES ($1, $2)`, id, status); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		ids := []uuid.UUID{failed, healthy}
		_, _ = pool.Exec(bg, `DELETE FROM video_metadata WHERE post_id = ANY($1)`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM post_outbox_events WHERE aggregate_id = ANY($1)`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = ANY($1)`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = ANY($1)`, ids)
	})

	listed, err := s.ListFailedScheduledVideos(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range listed {
		seen[id] = true
	}
	if !seen[failed] || seen[healthy] {
		t.Fatalf("listed failed=%v healthy=%v, want only the failed one", seen[failed], seen[healthy])
	}
	if ok, err := s.ResolveFailedScheduledVideo(ctx, healthy); err != nil || ok {
		t.Fatalf("healthy resolve = %v, %v; want untouched", ok, err)
	}
	if ok, err := s.ResolveFailedScheduledVideo(ctx, failed); err != nil || !ok {
		t.Fatalf("failed resolve = %v, %v", ok, err)
	}
	var scheduled bool
	if err := pool.QueryRow(ctx, `SELECT publish_at IS NOT NULL FROM posts WHERE id=$1`, failed).Scan(&scheduled); err != nil {
		t.Fatal(err)
	}
	if scheduled || reviewStatus(t, pool, failed) != "rejected" {
		t.Fatalf("failed video: scheduled=%v status=%s, want off the schedule and rejected", scheduled, reviewStatus(t, pool, failed))
	}
	notices := authorNotices(t, pool, failed)
	if len(notices) != 1 || notices[0].Kind != store.AuthorNoticeScheduledVideoFailed || notices[0].AuthorID != author.String() {
		t.Fatalf("notices = %+v", notices)
	}
	if ok, _ := s.ResolveFailedScheduledVideo(ctx, failed); ok || len(authorNotices(t, pool, failed)) != 1 {
		t.Fatal("a second resolve moved it again or told the creator twice")
	}
	if err := pool.QueryRow(ctx, `SELECT publish_at IS NOT NULL FROM posts WHERE id=$1`, healthy).Scan(&scheduled); err != nil || !scheduled {
		t.Fatalf("healthy video left the schedule (scheduled=%v err=%v)", scheduled, err)
	}
}
