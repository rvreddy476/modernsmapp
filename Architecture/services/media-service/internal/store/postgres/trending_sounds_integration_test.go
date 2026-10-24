//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Trending sounds (migration 030) against live PostgreSQL, on both shapes
// of audio_tracks, in a schema of its own (soundSchema):
//
//	POSTGRES_DSN=postgres://…/<name>_test go test -tags integration ./internal/store/postgres/ -run TrendingSounds -v
//
// Refuses any database that is not a scratch one (requireScratchDatabase).

func trendingSchema(t *testing.T, shape string) (*MediaAssetStore, func(sql string, args ...any)) {
	t.Helper()
	store, exec := librarySchema(t, shape)
	applyMediaMigration(t, store.db, "029_admin_sound_library.sql")
	applyMediaMigration(t, store.db, "030_trending_sounds.sql")
	// A replay must be harmless.
	applyMediaMigration(t, store.db, "030_trending_sounds.sql")
	return store, exec
}

func TestTrendingSoundsStoreRoundTrip(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			store, exec := trendingSchema(t, shape)
			ctx := context.Background()

			extracted := newSound(uuid.New(), "Kitchen take")
			if _, err := store.InsertSoundIfAbsent(ctx, extracted); err != nil {
				t.Fatal(err)
			}
			libRow := uuid.New()
			exec(`INSERT INTO audio_library (id, title, artist, duration_ms, audio_url, source, cover_url, first_activated_at, license_type)
				VALUES ($1, 'Monsoon Groove', 'Momentum Library', 30000, 'library/monsoon.m4a', 'licensed', 'https://cdn.example.com/m.jpg', NOW(), 'licensed')`, libRow)
			lib, err := store.EnsureLibrarySound(ctx, libRow, "library/monsoon.m4a")
			if err != nil {
				t.Fatal(err)
			}
			if lib.LibraryCoverURL == nil || *lib.CoverURL() != "https://cdn.example.com/m.jpg" {
				t.Fatalf("library cover %v", lib.LibraryCoverURL)
			}

			// Candidates carry the library facts the run judges.
			exec(`UPDATE audio_library SET license_expires_at = NOW() - INTERVAL '1 day' WHERE id = $1`, libRow)
			cands, err := store.TrendingCandidates(ctx, []uuid.UUID{extracted.ID, lib.ID, uuid.New()})
			if err != nil {
				t.Fatal(err)
			}
			if len(cands) != 2 || cands[extracted.ID].Library.Present || !cands[lib.ID].Library.Present ||
				!cands[lib.ID].Library.IsActive || cands[lib.ID].Library.LicenseExpiresAt == nil {
				t.Fatalf("candidates %+v", cands)
			}

			if run, err := store.LatestTrendingRun(ctx); err != nil || run != nil {
				t.Fatalf("before the first run: %+v %v", run, err)
			}
			at := time.Now().UTC().Truncate(time.Microsecond)
			gone := uuid.New() // deleted since the run read it: skipped, not a failed insert
			if err := store.ReplaceTrendingSounds(ctx, at, []TrendingSound{
				{AudioTrackID: extracted.ID, Rank: 1, Score: 30, Uses7d: 10, UsesPrev7d: 2, Rising: true},
				{AudioTrackID: gone, Rank: 2, Score: 20, Uses7d: 8},
				{AudioTrackID: lib.ID, Rank: 3, Score: 9, Uses7d: 3, UsesPrev7d: 3},
			}); err != nil {
				t.Fatal(err)
			}
			run, err := store.LatestTrendingRun(ctx)
			if err != nil || run == nil || !run.ComputedAt.Equal(at) || run.SoundCount != 3 {
				t.Fatalf("run %+v %v", run, err)
			}

			all, err := store.ListTrendingSounds(ctx, false, 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 2 || all[0].Track.ID != extracted.ID || all[0].Rank != 1 || all[0].Uses7d != 10 ||
				all[1].Track.ID != lib.ID || all[1].Rank != 3 || all[1].Library.LicenseExpiresAt == nil || all[1].Track.LibraryCoverURL == nil {
				t.Fatalf("list %+v", all)
			}
			if all[0].Track.Title != "Kitchen take" || all[0].Track.CreatorUserID == nil {
				t.Fatalf("the sound was not read back whole: %+v", all[0].Track)
			}
			rising, _ := store.ListTrendingSounds(ctx, true, 0, 10)
			if len(rising) != 1 || rising[0].Track.ID != extracted.ID {
				t.Fatalf("rising %+v", rising)
			}
			after, _ := store.ListTrendingSounds(ctx, false, 1, 10)
			if len(after) != 1 || after[0].Rank != 3 {
				t.Fatalf("after rank 1: %+v", after)
			}
			limited, _ := store.ListTrendingSounds(ctx, false, 0, 1)
			if len(limited) != 1 {
				t.Fatalf("limit 1: %d rows", len(limited))
			}

			one, err := store.TrendingSoundByID(ctx, lib.ID)
			if err != nil || one == nil || one.Rank != 3 || one.Rising {
				t.Fatalf("by id %+v %v", one, err)
			}
			if none, err := store.TrendingSoundByID(ctx, uuid.New()); none != nil || err != nil {
				t.Fatalf("an unranked sound: %+v %v", none, err)
			}

			// The next run replaces the whole list.
			if err := store.ReplaceTrendingSounds(ctx, at.Add(15*time.Minute), []TrendingSound{
				{AudioTrackID: lib.ID, Rank: 1, Score: 12, Uses7d: 4},
			}); err != nil {
				t.Fatal(err)
			}
			all, _ = store.ListTrendingSounds(ctx, false, 0, 10)
			if len(all) != 1 || all[0].Track.ID != lib.ID || all[0].Rank != 1 {
				t.Fatalf("after the second run %+v", all)
			}
			if err := store.ReplaceTrendingSounds(ctx, at.Add(30*time.Minute), nil); err != nil {
				t.Fatal(err)
			}
			if all, _ = store.ListTrendingSounds(ctx, false, 0, 10); len(all) != 0 {
				t.Fatalf("an empty run left %d rows", len(all))
			}

			// A sound deleted outright leaves the ranking with it.
			if err := store.ReplaceTrendingSounds(ctx, at.Add(45*time.Minute), []TrendingSound{{AudioTrackID: extracted.ID, Rank: 1, Score: 9, Uses7d: 3}}); err != nil {
				t.Fatal(err)
			}
			exec(`DELETE FROM audio_tracks WHERE id = $1`, extracted.ID)
			if all, _ = store.ListTrendingSounds(ctx, false, 0, 10); len(all) != 0 {
				t.Fatalf("a deleted sound is still ranked")
			}
		})
	}
}

// One replica runs at a time: a second lease attempt while the first is
// held is refused, and the lease is free again afterwards.
func TestTrendingSoundsLease(t *testing.T) {
	store, _ := trendingSchema(t, mediaFirstAudioTracks)
	ctx := context.Background()
	var inner bool
	held, err := store.WithTrendingLease(ctx, func(ctx context.Context) error {
		var err error
		inner, err = store.WithTrendingLease(ctx, func(context.Context) error {
			t.Fatal("a second replica ran while the first held the lease")
			return nil
		})
		return err
	})
	if err != nil || !held || inner {
		t.Fatalf("held %v inner %v err %v", held, inner, err)
	}
	again, err := store.WithTrendingLease(ctx, func(context.Context) error { return nil })
	if err != nil || !again {
		t.Fatalf("the lease was not released: %v %v", again, err)
	}
}
