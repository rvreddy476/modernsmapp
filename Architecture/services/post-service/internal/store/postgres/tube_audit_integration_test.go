//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The tube backend audit (2026-10-30) against a real Postgres, so the SQL
// halves of its fixes and migrations 080 / 081 are themselves under test:
//
//	VIDEO_SERIES_POSTGRES_DSN=postgres://…/post_it_test?sslmode=disable \
//	  go test -tags integration ./internal/store/postgres/ -run TubeAudit
//
// CI only (.github/workflows/integration-all.yml), on a _test database.

func tubePlaylist(t *testing.T, pool *pgxpool.Pool, st *postgres.Store, owner uuid.UUID) *postgres.Playlist {
	t.Helper()
	p := &postgres.Playlist{CreatorID: owner, Title: "tube audit", Visibility: "public"}
	if err := st.CreatePlaylist(context.Background(), p); err != nil {
		t.Fatalf("create playlist: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM playlists WHERE id = $1`, p.ID) })
	return p
}

func allPlaylistItems(t *testing.T, st *postgres.Store, id uuid.UUID) []postgres.PlaylistItem {
	t.Helper()
	items, err := st.GetPlaylistItems(context.Background(), id, postgres.PlaylistItemsPage{Limit: postgres.MaxPlaylistItemsPage})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// M1: twenty devices adding at once all land, at distinct positions, and
// item_count agrees. Before the parent lock they computed the same position
// and overwrote each other.
func TestTubeAudit_ConcurrentPlaylistAddsAllLand(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	owner := newSeriesCreator(t, pool)
	p := tubePlaylist(t, pool, st, owner)

	const n = 20
	posts := make([]uuid.UUID, n)
	for i := range posts {
		posts[i] = insertSeriesPost(t, pool, owner, nil, nil)
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, post := range posts {
		wg.Add(1)
		go func(post uuid.UUID) {
			defer wg.Done()
			if _, err := st.AddPlaylistItem(ctx, p.ID, post, nil, postgres.MaxPlaylistItems); err != nil {
				errs <- err
			}
		}(post)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent add: %v", err)
	}
	items := allPlaylistItems(t, st, p.ID)
	if len(items) != n {
		t.Fatalf("%d items after %d concurrent adds; adds were lost", len(items), n)
	}
	seen := map[uuid.UUID]bool{}
	for i, it := range items {
		if it.Position != i {
			t.Fatalf("positions %+v; want 0..%d", items, n-1)
		}
		seen[it.PostID] = true
	}
	if len(seen) != n {
		t.Fatalf("%d distinct posts; want %d", len(seen), n)
	}
	got, err := st.GetPlaylist(ctx, p.ID)
	if err != nil || got.ItemCount != n {
		t.Fatalf("item_count %d (err %v); want %d", got.ItemCount, err, n)
	}
}

// M1: a duplicate is refused, an insert shifts instead of overwriting, and
// the cap holds.
func TestTubeAudit_PlaylistAddNeverOverwrites(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	owner := newSeriesCreator(t, pool)
	p := tubePlaylist(t, pool, st, owner)
	a, b, c := insertSeriesPost(t, pool, owner, nil, nil), insertSeriesPost(t, pool, owner, nil, nil), insertSeriesPost(t, pool, owner, nil, nil)

	for _, post := range []uuid.UUID{a, b} {
		if _, err := st.AddPlaylistItem(ctx, p.ID, post, nil, 3); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AddPlaylistItem(ctx, p.ID, a, nil, 3); !errors.Is(err, postgres.ErrPlaylistItemExists) {
		t.Fatalf("adding a listed post again: err=%v want ErrPlaylistItemExists", err)
	}
	zero := 0
	item, err := st.AddPlaylistItem(ctx, p.ID, c, &zero, 3)
	if err != nil || item.Position != 0 {
		t.Fatalf("insert at 0: %+v %v", item, err)
	}
	items := allPlaylistItems(t, st, p.ID)
	if len(items) != 3 || items[0].PostID != c || items[1].PostID != a || items[2].PostID != b {
		t.Fatalf("after an insert at 0: %+v; want c, a, b (nothing overwritten)", items)
	}
	if _, err := st.AddPlaylistItem(ctx, p.ID, insertSeriesPost(t, pool, owner, nil, nil), nil, 3); !errors.Is(err, postgres.ErrPlaylistFull) {
		t.Fatalf("a fourth item in a playlist capped at 3: err=%v want ErrPlaylistFull", err)
	}
	// Paging: one row after position 0.
	after := 0
	page, err := st.GetPlaylistItems(ctx, p.ID, postgres.PlaylistItemsPage{AfterPosition: &after, Limit: 1})
	if err != nil || len(page) != 1 || page[0].PostID != a {
		t.Fatalf("page after 0: %+v %v; want a", page, err)
	}
}

// M1: a move and an add racing never lose the add.
func TestTubeAudit_MoveAndAddRaceKeepsTheAdd(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	owner := newSeriesCreator(t, pool)
	p := tubePlaylist(t, pool, st, owner)
	var first uuid.UUID
	for i := 0; i < 5; i++ {
		post := insertSeriesPost(t, pool, owner, nil, nil)
		if i == 0 {
			first = post
		}
		if _, err := st.AddPlaylistItem(ctx, p.ID, post, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	extra := make([]uuid.UUID, 10)
	for i := range extra {
		extra[i] = insertSeriesPost(t, pool, owner, nil, nil)
	}
	var wg sync.WaitGroup
	for i, post := range extra {
		wg.Add(2)
		go func(post uuid.UUID) {
			defer wg.Done()
			if _, err := st.AddPlaylistItem(ctx, p.ID, post, nil, 0); err != nil {
				t.Errorf("add: %v", err)
			}
		}(post)
		go func(to int) {
			defer wg.Done()
			if _, err := st.MovePlaylistItem(ctx, p.ID, first, to); err != nil {
				t.Errorf("move: %v", err)
			}
		}(i % 4)
	}
	wg.Wait()
	if items := allPlaylistItems(t, st, p.ID); len(items) != 15 {
		t.Fatalf("%d items after 10 adds racing 10 moves on 5; want 15 (an add was lost)", len(items))
	}
}

// H2: a stranger's video is refused inside the transaction, and rows written
// before the guard are never read back.
func TestTubeAudit_SeriesEpisodesAreTheCreatorsOwn(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	attacker, victim := newSeriesCreator(t, pool), newSeriesCreator(t, pool)
	vs := &postgres.VideoSeries{CreatorID: attacker, Title: "scam", IsPublic: true}
	if err := st.CreateVideoSeries(ctx, vs); err != nil {
		t.Fatal(err)
	}
	victimPost := insertSeriesPost(t, pool, victim, nil, nil)
	if _, err := st.AddEpisodeToVideoSeries(ctx, vs.ID, victimPost, 1, nil); !errors.Is(err, postgres.ErrEpisodeNotByCreator) {
		t.Fatalf("a stranger's video: err=%v want ErrEpisodeNotByCreator", err)
	}
	// A row written before the guard existed.
	if _, err := pool.Exec(ctx, `INSERT INTO video_series_episodes (series_id, post_id, episode_num) VALUES ($1, $2, 1)`, vs.ID, victimPost); err != nil {
		t.Fatal(err)
	}
	if m, err := st.FindSeriesMembershipsByPost(ctx, victimPost); err != nil || len(m) != 0 {
		t.Fatalf("the victim's video resolves to %d foreign memberships (err %v)", len(m), err)
	}
	for _, owner := range []bool{true, false} {
		if eps, err := st.GetVideoSeriesEpisodes(ctx, vs.ID, owner); err != nil || len(eps) != 0 {
			t.Fatalf("episodes (owner view %v): %d foreign rows listed (err %v)", owner, len(eps), err)
		}
	}
	own := insertSeriesPost(t, pool, attacker, nil, nil)
	if _, err := st.AddEpisodeToVideoSeries(ctx, vs.ID, own, 2, nil); err != nil {
		t.Fatalf("the creator's own video: %v", err)
	}
}

// L3: a late report does not move the resume point backwards.
func TestTubeAudit_StaleProgressIsNotApplied(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	user := newSeriesCreator(t, pool)
	post := insertSeriesPost(t, pool, user, nil, nil)
	at := func(d time.Duration) *time.Time { v := time.Now().UTC().Add(d); return &v }

	newer := &postgres.WatchProgress{UserID: user, PostID: post, PositionMs: 90000, DurationMs: 600000, ClientSentAt: at(0)}
	if applied, err := st.UpsertWatchProgress(ctx, newer); err != nil || !applied {
		t.Fatalf("first report: applied %v err %v", applied, err)
	}
	late := &postgres.WatchProgress{UserID: user, PostID: post, PositionMs: 10000, DurationMs: 600000, ClientSentAt: at(-time.Minute)}
	applied, err := st.UpsertWatchProgress(ctx, late)
	if err != nil || applied {
		t.Fatalf("late report: applied %v err %v; want not applied", applied, err)
	}
	if late.PositionMs != 90000 {
		t.Fatalf("the late report answered position %d; want the stored 90000", late.PositionMs)
	}
	stored, _ := st.GetWatchProgress(ctx, user, post)
	if stored.PositionMs != 90000 {
		t.Fatalf("stored position %d; a late heartbeat moved it backwards", stored.PositionMs)
	}
	// An older client (no time) is applied as before.
	legacy := &postgres.WatchProgress{UserID: user, PostID: post, PositionMs: 120000, DurationMs: 600000}
	if applied, err := st.UpsertWatchProgress(ctx, legacy); err != nil || !applied {
		t.Fatalf("a report without sent_at: applied %v err %v", applied, err)
	}
}

// H1: the composer queue backs off (migration 080).
func TestTubeAudit_ComposerDraftQueueBacksOff(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	author := uuid.New()
	now := time.Now().UTC()
	mk := func(scheduleAt time.Time) *postgres.PostDraft {
		d := &postgres.PostDraft{AuthorID: author, PostType: "post", Payload: json.RawMessage(`{"text":"q"}`), ScheduleAt: &scheduleAt}
		if err := st.CreatePostDraft(ctx, d, nil); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM post_drafts WHERE id = $1`, d.ID) })
		return d
	}
	waiting := mk(now.Add(-2 * time.Hour))
	due := mk(now.Add(-time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE post_drafts SET next_attempt_at = $2 WHERE id = $1`, waiting.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimDuePostDrafts(ctx, now, 10*time.Minute, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var got *postgres.PostDraft
	for i := range claimed {
		switch claimed[i].ID {
		case waiting.ID:
			t.Fatal("a draft waiting on its next attempt was claimed")
		case due.ID:
			got = &claimed[i]
		}
	}
	if got == nil {
		t.Fatal("the due draft was not claimed")
	}
	plan := func(attempts int) (time.Time, string) {
		if attempts >= 2 {
			return time.Time{}, "publish_failed"
		}
		return now.Add(time.Hour), ""
	}
	if parked, err := st.DeferPostDraftPublish(ctx, got.ID, got.ClaimToken, plan); err != nil || parked {
		t.Fatalf("first failure: parked %v err %v", parked, err)
	}
	var status string
	var attempts int
	var next *time.Time
	row := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT status, publish_attempts, next_attempt_at FROM post_drafts WHERE id = $1`, got.ID).
			Scan(&status, &attempts, &next); err != nil {
			t.Fatal(err)
		}
	}
	row()
	if status != "draft" || attempts != 1 || next == nil {
		t.Fatalf("after one failure: %s, %d attempts, next %v", status, attempts, next)
	}
	// A stale token is not this worker's claim: nothing changes.
	stale := uuid.New()
	if _, err := pool.Exec(ctx, `UPDATE post_drafts SET status = 'publishing', claim_token = gen_random_uuid() WHERE id = $1`, got.ID); err != nil {
		t.Fatal(err)
	}
	if parked, err := st.DeferPostDraftPublish(ctx, got.ID, &stale, plan); err != nil || parked {
		t.Fatalf("a stale claim: parked %v err %v", parked, err)
	}
	row()
	if attempts != 1 {
		t.Fatalf("a stale claim counted an attempt (%d)", attempts)
	}
	if parked, err := st.DeferPostDraftPublish(ctx, got.ID, nil, plan); err != nil || !parked {
		t.Fatalf("second failure: parked %v err %v; want parked", parked, err)
	}
	row()
	if status != "blocked" {
		t.Fatalf("after the cap: %s; want blocked", status)
	}
	// An edit with a new time revives it with the backoff cleared.
	later := now.Add(2 * time.Hour)
	if ok, err := st.UpdatePostDraft(ctx, got.ID, author, "post", json.RawMessage(`{"text":"fixed"}`), &later, nil); err != nil || !ok {
		t.Fatalf("edit: %v %v", ok, err)
	}
	row()
	if status != "draft" || attempts != 0 || next != nil {
		t.Fatalf("after an edit: %s, %d attempts, next %v; want a fresh draft", status, attempts, next)
	}
}
