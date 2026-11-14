//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Echo v2 store rules against a real Postgres (migration 083). CI only:
//
//	POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/store/postgres/ -run NoteV2 -v

func seedV2Note(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, mod string, args ...any) uuid.UUID {
	t.Helper()
	id := seedNote(t, pool, author, nil, nil, 0)
	if mod != "" {
		if _, err := pool.Exec(context.Background(), `UPDATE posts SET `+mod+` WHERE id = $1`, append([]any{id}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM note_views WHERE note_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM note_schedules WHERE note_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM saved_items WHERE target_id = $1`, id)
	})
	return id
}

func viewCount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), `SELECT note_view_count FROM posts WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNoteV2ViewsOncePerDayNeverTheAuthor(t *testing.T) {
	pool := openSubscriptionDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	newUser(t, pool, alice)
	newUser(t, pool, bob)
	id := seedV2Note(t, pool, alice, "")
	now := time.Now().UTC()
	if n, _ := st.RecordNoteViews(ctx, alice, []uuid.UUID{id}, now); n != 0 {
		t.Fatal("the author's view counted")
	}
	if n, _ := st.RecordNoteViews(ctx, bob, []uuid.UUID{id, id}, now); n != 1 {
		t.Fatalf("bob's first view counted %d", n)
	}
	if n, _ := st.RecordNoteViews(ctx, bob, []uuid.UUID{id}, now.Add(23*time.Hour)); n != 0 {
		t.Fatal("counted again within 24 hours")
	}
	if n, _ := st.RecordNoteViews(ctx, bob, []uuid.UUID{id}, now.Add(24*time.Hour)); n != 1 {
		t.Fatal("not counted after 24 hours")
	}
	if got := viewCount(t, pool, id); got != 2 {
		t.Fatalf("note_view_count %d", got)
	}
	sched := seedV2Note(t, pool, alice, "publish_at = $2", now.Add(time.Hour))
	if n, _ := st.RecordNoteViews(ctx, bob, []uuid.UUID{sched}, now); n != 0 {
		t.Fatal("a scheduled note took a view")
	}
}

func TestNoteV2ScheduledNotesAreInNoRead(t *testing.T) {
	pool := openSubscriptionDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	alice := uuid.New()
	newUser(t, pool, alice)
	at := time.Now().UTC().Add(time.Hour)
	id := seedV2Note(t, pool, alice, "publish_at = $2, hashtags = ARRAY['v2sched'], quote_of_id = NULL", at)
	got, err := st.GetNotes(ctx, []uuid.UUID{id})
	if err != nil || got[id] != nil {
		t.Fatalf("GetNotes returned a scheduled note: %v", err)
	}
	for _, q := range []postgres.NoteDiscoverQuery{{Hashtag: "v2sched"}, {Search: "note"}} {
		rows, _, err := st.DiscoverNotes(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.Post.ID == id {
				t.Fatalf("%+v lists the scheduled note", q)
			}
		}
	}
	page, _, err := st.ListScheduledNotes(ctx, alice, "", 20)
	if err != nil || len(page) != 1 || page[0].Post.ID != id {
		t.Fatalf("scheduled list: %v %d", err, len(page))
	}
	posts, _, err := st.ListScheduledPostsByAuthor(ctx, alice, 20, "")
	if err != nil || len(posts) != 0 {
		t.Fatalf("the post Scheduled list shows a note: %v %d", err, len(posts))
	}
	due, err := st.ListDueScheduledPosts(ctx, at.Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range due {
		if c.PostID == id {
			t.Fatal("the post schedule worker would take a note")
		}
	}
}

func TestNoteV2ClaimIsDisjointAndPublishIsOnce(t *testing.T) {
	pool := openSubscriptionDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	alice := uuid.New()
	newUser(t, pool, alice)
	now := time.Now().UTC()
	var ids []uuid.UUID
	for i := 0; i < 6; i++ {
		ids = append(ids, seedV2Note(t, pool, alice, "publish_at = $2", now.Add(-time.Minute)))
	}
	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := st.ClaimDueNotes(ctx, now, now.Add(10*time.Minute), 100)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, id := range got {
				seen[id]++
			}
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("note claimed %d times", seen[id])
		}
	}
	// Leased: not claimed again before the lease ends.
	if again, _ := st.ClaimDueNotes(ctx, now.Add(time.Minute), now.Add(11*time.Minute), 100); containsID(again, ids[0]) {
		t.Fatal("a leased note was claimed again")
	}
	res, err := st.PublishScheduledNote(ctx, &postgres.NotePublish{ID: ids[0], Now: now})
	if err != nil || res == nil {
		t.Fatalf("publish: %v", err)
	}
	if res, _ := st.PublishScheduledNote(ctx, &postgres.NotePublish{ID: ids[0], Now: now}); res != nil {
		t.Fatal("published twice")
	}
	if got, _ := st.GetNotes(ctx, []uuid.UUID{ids[0]}); got[ids[0]] == nil {
		t.Fatal("the published note is not readable")
	}
	// Failure state: retried, then failed after 24 hours.
	failed, err := st.RecordNotePublishFailure(ctx, ids[1], "premium_required", now, now.Add(time.Hour), now.Add(-24*time.Hour))
	if err != nil || failed {
		t.Fatalf("first failure: %v failed=%v", err, failed)
	}
	later := now.Add(24 * time.Hour)
	if failed, _ := st.RecordNotePublishFailure(ctx, ids[1], "premium_required", later, later.Add(time.Hour), later.Add(-24*time.Hour)); !failed {
		t.Fatal("not failed after 24 hours")
	}
	if got, _ := st.ClaimDueNotes(ctx, later.Add(2*time.Hour), later.Add(3*time.Hour), 100); containsID(got, ids[1]) {
		t.Fatal("a failed note was claimed")
	}
	if ok, err := st.RescheduleNote(ctx, ids[1], alice, later.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("reschedule: %v", err)
	}
	if sn, _ := st.GetScheduledNote(ctx, ids[1]); sn == nil || sn.Failed || sn.PublishError != nil {
		t.Fatalf("reschedule did not re-arm: %+v", sn)
	}
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestNoteV2TrendsNeedThreeAuthors(t *testing.T) {
	pool := openSubscriptionDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	authors := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, a := range authors {
		newUser(t, pool, a)
	}
	tag := "v2trend" + uuid.NewString()[:8]
	duo := "v2duo" + uuid.NewString()[:8]
	for _, a := range authors {
		seedV2Note(t, pool, a, "hashtags = ARRAY[$2]", tag)
	}
	seedV2Note(t, pool, authors[0], "hashtags = ARRAY[$2]", duo)
	seedV2Note(t, pool, authors[1], "hashtags = ARRAY[$2]", duo)
	rows, err := st.NoteTrends(ctx, time.Now().Add(-24*time.Hour), 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.Tag == duo {
			t.Fatal("a two-author tag trends")
		}
		if r.Tag == tag && r.Authors == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("a three-author tag does not trend")
	}
}
