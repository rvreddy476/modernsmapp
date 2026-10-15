//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

/*
Comment report -> auto-review hold (T-13) against a real Postgres (*_test
only): the trust-safety ReportFiled path counts each reporter once per
comment, ignores redelivery, holds at the threshold exactly once, and moves
comment_count in the same transaction.

Run:

	POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/store/postgres/ -run CommentReportHold -v
*/
func TestCommentReportHoldCountsEachReporterOnce(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	author, commenter := uuid.New(), uuid.New()
	postID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at) VALUES ($1, $2, 'report me', 'public', 'post', now(), now())`,
		postID, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM comment_report_reporters WHERE comment_id IN (SELECT id FROM comments WHERE post_id = $1)`, postID)
		pool.Exec(bg, `DELETE FROM comments WHERE post_id = $1 AND parent_id IS NOT NULL`, postID)
		pool.Exec(bg, `DELETE FROM comments WHERE post_id = $1`, postID)
		pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, postID)
	})
	c, err := store.CreateComment(ctx, postID, commenter, "the comment")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateComment(ctx, postID, commenter, "another")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdjustCommentCount(ctx, postID, 2); err != nil {
		t.Fatal(err)
	}
	state := func() (int, string, int64) {
		t.Helper()
		var flagged int
		var status string
		if err := pool.QueryRow(ctx, `SELECT flagged_count, moderation_status FROM comments WHERE id = $1`, c.ID).Scan(&flagged, &status); err != nil {
			t.Fatal(err)
		}
		n, err := store.GetCommentCount(ctx, postID)
		if err != nil {
			t.Fatal(err)
		}
		return flagged, status, n
	}

	r1, r2, r3 := uuid.New(), uuid.New(), uuid.New()
	rep1, rep2, rep3 := uuid.New(), uuid.New(), uuid.New()

	out, err := store.ApplyCommentReportHold(ctx, rep1, c.ID, r1)
	if err != nil || !out.Found || !out.Counted || out.Held || out.PostID != postID {
		t.Fatalf("first report: %+v %v", out, err)
	}
	// Kafka redelivers the same report: nothing moves.
	for i := 0; i < 3; i++ {
		if out, err := store.ApplyCommentReportHold(ctx, rep1, c.ID, r1); err != nil || out.Counted {
			t.Fatalf("redelivery %d counted: %+v %v", i, out, err)
		}
	}
	// The same reporter files a second report later: still one.
	if out, err := store.ApplyCommentReportHold(ctx, uuid.New(), c.ID, r1); err != nil || out.Counted {
		t.Fatalf("repeat reporter counted: %+v %v", out, err)
	}
	if flagged, status, n := state(); flagged != 1 || status != "visible" || n != 2 {
		t.Fatalf("after one reporter: flagged=%d status=%s count=%d", flagged, status, n)
	}

	if out, err := store.ApplyCommentReportHold(ctx, rep2, c.ID, r2); err != nil || !out.Counted || out.Held {
		t.Fatalf("second reporter: %+v %v", out, err)
	}
	// The third distinct reporter reaches the threshold: held, out of the count.
	out, err = store.ApplyCommentReportHold(ctx, rep3, c.ID, r3)
	if err != nil || !out.Counted || !out.Held || !out.LeftCount {
		t.Fatalf("third reporter: %+v %v", out, err)
	}
	if flagged, status, n := state(); flagged != 3 || status != "review" || n != 1 {
		t.Fatalf("after the threshold: flagged=%d status=%s count=%d, want 3/review/1", flagged, status, n)
	}
	if direct, err := store.CountVisibleComments(ctx, postID); err != nil || direct != 1 {
		t.Fatalf("stored count disagrees with the definition: %d (%v)", direct, err)
	}
	// Redelivering the report that held it does not hold or decrement again.
	if out, err := store.ApplyCommentReportHold(ctx, rep3, c.ID, r3); err != nil || out.Counted || out.Held {
		t.Fatalf("redelivered hold: %+v %v", out, err)
	}
	// A fourth reporter counts but cannot hold twice.
	if out, err := store.ApplyCommentReportHold(ctx, uuid.New(), c.ID, uuid.New()); err != nil || !out.Counted || out.Held {
		t.Fatalf("fourth reporter: %+v %v", out, err)
	}
	if _, _, n := state(); n != 1 {
		t.Fatalf("count moved after the hold: %d", n)
	}

	// Concurrent redeliveries of fresh reports on another comment: each
	// distinct reporter counts exactly once, and the hold happens once.
	reporters := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	var wg sync.WaitGroup
	var mu sync.Mutex
	holds := 0
	for _, r := range reporters {
		report := uuid.New()
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(report, r uuid.UUID) {
				defer wg.Done()
				out, err := store.ApplyCommentReportHold(ctx, report, other.ID, r)
				if err != nil {
					t.Errorf("concurrent apply: %v", err)
					return
				}
				if out.Held {
					mu.Lock()
					holds++
					mu.Unlock()
				}
			}(report, r)
		}
	}
	wg.Wait()
	var flagged int
	var status string
	if err := pool.QueryRow(ctx, `SELECT flagged_count, moderation_status FROM comments WHERE id = $1`, other.ID).Scan(&flagged, &status); err != nil {
		t.Fatal(err)
	}
	if flagged != len(reporters) || status != "review" || holds != 1 {
		t.Fatalf("concurrent: flagged=%d status=%s holds=%d, want %d/review/1", flagged, status, holds, len(reporters))
	}
	if n, _ := store.GetCommentCount(ctx, postID); n != 0 {
		t.Fatalf("comment_count after both holds = %d, want 0", n)
	}

	// A comment post-service does not hold: nothing found, nothing recorded.
	if out, err := store.ApplyCommentReportHold(ctx, uuid.New(), uuid.New(), uuid.New()); err != nil || out.Found || out.Counted {
		t.Fatalf("unknown comment: %+v %v", out, err)
	}
}
