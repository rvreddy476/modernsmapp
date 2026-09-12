//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Video series against a real Postgres (2026-09-12).
//
//	VIDEO_SERIES_POSTGRES_DSN=postgres://… go test -tags integration ./internal/store/postgres/ -run VideoSeries
//
// Same shape as scheduled_integration_test.go: the real schema path
// (setup.sql + every migration) on a scratch database. video_series has FKs
// into users, channels and media_assets, which belong to other services, so
// minimal parents are created here the way openScheduleDB creates
// media_assets. Use a scratch database: the parents are created with
// IF NOT EXISTS and never dropped.

func openVideoSeriesDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("VIDEO_SERIES_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("VIDEO_SERIES_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS channels (id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY,
			uploader_id UUID NOT NULL,
			file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			pool.Close()
			t.Fatal(err)
		}
	}
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		pool.Close()
		t.Fatalf("bootstrap schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newSeriesCreator(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (id) VALUES ($1)`, id); err != nil {
		t.Fatalf("insert creator: %v", err)
	}
	return id
}

// insertSeriesPost writes the minimum posts row an episode can point at.
// deletedAt and publishAt are the two columns the non-owner episode read
// filters on (migrations 007 and 042).
func insertSeriesPost(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, deletedAt, publishAt *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at, deleted_at, publish_at)
		VALUES ($1, $2, 'series proof', 'public', 'video', NOW(), NOW(), $3, $4)`,
		id, author, deletedAt, publishAt)
	if err != nil {
		t.Fatalf("insert post: %v", err)
	}
	return id
}

func TestVideoSeries_EpisodeReadHidesUnpublishedFromNonOwners(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	creator := newSeriesCreator(t, pool)

	vs := &postgres.VideoSeries{CreatorID: creator, Title: "proof", IsPublic: true}
	if err := st.CreateVideoSeries(ctx, vs); err != nil {
		t.Fatalf("create series: %v", err)
	}

	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	live := insertSeriesPost(t, pool, creator, nil, nil)
	deleted := insertSeriesPost(t, pool, creator, &past, nil)
	scheduled := insertSeriesPost(t, pool, creator, nil, &future)
	for n, p := range map[int]uuid.UUID{1: live, 2: deleted, 3: scheduled} {
		if _, err := st.AddEpisodeToVideoSeries(ctx, vs.ID, p, n, nil); err != nil {
			t.Fatalf("add episode %d: %v", n, err)
		}
	}

	nums := func(eps []postgres.VideoSeriesEpisode) []int {
		out := make([]int, 0, len(eps))
		for _, ep := range eps {
			out = append(out, ep.EpisodeNum)
		}
		return out
	}

	owner, err := st.GetVideoSeriesEpisodes(ctx, vs.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := nums(owner); len(got) != 3 {
		t.Fatalf("owner's read: episodes %v; want all three, the deleted and scheduled ones included", got)
	}

	public, err := st.GetVideoSeriesEpisodes(ctx, vs.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := nums(public); len(got) != 1 || got[0] != 1 {
		t.Fatalf("non-owner's read: episodes %v; want [1] only (episode 2's post is deleted, episode 3's is scheduled)", got)
	}
}

func TestVideoSeries_MembershipsNewestFirstAndCap(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	creator := newSeriesCreator(t, pool)
	post := insertSeriesPost(t, pool, creator, nil, nil)

	older := &postgres.VideoSeries{CreatorID: creator, Title: "older", IsPublic: true}
	newer := &postgres.VideoSeries{CreatorID: creator, Title: "newer", IsPublic: true}
	for _, vs := range []*postgres.VideoSeries{older, newer} {
		if err := st.CreateVideoSeries(ctx, vs); err != nil {
			t.Fatalf("create series: %v", err)
		}
	}
	if _, err := st.AddEpisodeToVideoSeries(ctx, older.ID, post, 1, nil); err != nil {
		t.Fatal(err)
	}
	// added_at defaults to NOW() at statement time; a second row in the same
	// millisecond would tie, so the older membership is pushed back.
	if _, err := pool.Exec(ctx, `UPDATE video_series_episodes SET added_at = NOW() - INTERVAL '1 hour' WHERE series_id = $1`, older.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddEpisodeToVideoSeries(ctx, newer.ID, post, 2, nil); err != nil {
		t.Fatal(err)
	}

	ms, err := st.FindSeriesMembershipsByPost(ctx, post)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].SeriesID != newer.ID || ms[1].SeriesID != older.ID {
		t.Fatalf("memberships: %v; want the newer series first", ms)
	}

	// The cap: fill the newer series to MaxSeriesEpisodes, then one more.
	for n := 3; n <= postgres.MaxSeriesEpisodes; n++ {
		p := insertSeriesPost(t, pool, creator, nil, nil)
		if _, err := st.AddEpisodeToVideoSeries(ctx, newer.ID, p, n, nil); err != nil {
			t.Fatalf("add episode %d: %v", n, err)
		}
	}
	// Episode 1 of the newer series was never added, so the series holds
	// MaxSeriesEpisodes-1 rows (2..50); episode 1 is growth and fits.
	if _, err := st.AddEpisodeToVideoSeries(ctx, newer.ID, insertSeriesPost(t, pool, creator, nil, nil), 1, nil); err != nil {
		t.Fatalf("filling the last free slot: %v", err)
	}
	_, err = st.AddEpisodeToVideoSeries(ctx, newer.ID, insertSeriesPost(t, pool, creator, nil, nil), postgres.MaxSeriesEpisodes+1, nil)
	if !errors.Is(err, postgres.ErrVideoSeriesFull) {
		t.Fatalf("episode %d of a full series: err=%v want ErrVideoSeriesFull", postgres.MaxSeriesEpisodes+1, err)
	}
	// An overwrite of an occupied number is not growth.
	title := "recut"
	if _, err := st.AddEpisodeToVideoSeries(ctx, newer.ID, insertSeriesPost(t, pool, creator, nil, nil), 7, &title); err != nil {
		t.Fatalf("overwriting episode 7 of a full series: %v; want it accepted", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT episode_count FROM video_series WHERE id = $1`, newer.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != postgres.MaxSeriesEpisodes {
		t.Fatalf("episode_count = %d; want %d", count, postgres.MaxSeriesEpisodes)
	}
}

func TestVideoSeries_UpdateCoalescesPerField(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	creator := newSeriesCreator(t, pool)

	vs := &postgres.VideoSeries{CreatorID: creator, Title: "before", Description: "keep me", IsPublic: false}
	if err := st.CreateVideoSeries(ctx, vs); err != nil {
		t.Fatal(err)
	}
	title, public := "after", true
	got, err := st.UpdateVideoSeries(ctx, vs.ID, postgres.VideoSeriesPatch{Title: &title, IsPublic: &public})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Title != "after" || !got.IsPublic || got.Description != "keep me" {
		t.Fatalf("after patch: %+v; want title and is_public changed, description untouched", got)
	}
	if missing, err := st.UpdateVideoSeries(ctx, uuid.New(), postgres.VideoSeriesPatch{Title: &title}); err != nil || missing != nil {
		t.Fatalf("patching a series that is not there: got=%v err=%v; want nil, nil", missing, err)
	}
}
