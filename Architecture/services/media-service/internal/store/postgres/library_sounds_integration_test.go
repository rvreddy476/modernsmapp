//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Library sounds (migration 028) against live PostgreSQL, on both shapes of
// audio_tracks, in a schema of its own (soundSchema):
//
//	POSTGRES_DSN=postgres://…/<name>_test go test -tags integration ./internal/store/postgres/ -run LibrarySound -v
//
// The property under test: a library row becomes ONE audio_tracks row on
// its first use, whoever and however many ask at once; only an active row
// is materialised; the library list reads its uses back from that row.

// audioLibraryDDL is audio_library as migration 006 creates it.
const audioLibraryDDL = `
CREATE TABLE audio_library (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title          TEXT NOT NULL,
    artist         TEXT NOT NULL DEFAULT '',
    duration_ms    INT NOT NULL,
    waveform_data  JSONB,
    cover_url      TEXT,
    audio_url      TEXT NOT NULL,
    source         TEXT NOT NULL CHECK (source IN ('licensed','original','user_upload')),
    source_post_id UUID,
    source_user_id UUID,
    usage_count    BIGINT NOT NULL DEFAULT 0,
    is_trending    BOOLEAN NOT NULL DEFAULT FALSE,
    is_licensed    BOOLEAN NOT NULL DEFAULT FALSE,
    language       TEXT DEFAULT 'instrumental',
    genre          TEXT,
    mood           TEXT,
    is_active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

func librarySchema(t *testing.T, shape string) (*MediaAssetStore, func(sql string, args ...any)) {
	t.Helper()
	pool := soundSchema(t, shape+audioLibraryDDL)
	applySoundMigration(t, pool)
	// The runner applies a file once; a replay must be harmless.
	applyMediaMigration(t, pool, "028_library_sounds.sql")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	return New(pool), exec
}

func TestLibrarySoundMaterialisesOnce(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			store, exec := librarySchema(t, shape)
			ctx := context.Background()
			active, inactive, untagged := uuid.New(), uuid.New(), uuid.New()
			exec(`INSERT INTO audio_library (id, title, artist, duration_ms, audio_url, source, is_licensed, genre, usage_count)
				VALUES ($1, 'Monsoon Groove', 'Momentum Library', 30000, 'library/monsoon.m4a', 'licensed', TRUE, 'pop', 2)`, active)
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source, is_active)
				VALUES ($1, 'Retired', 1000, 'library/retired.m4a', 'licensed', FALSE)`, inactive)
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source)
				VALUES ($1, 'No genre', 1000, 'library/plain.m4a', 'original')`, untagged)

			first, err := store.EnsureLibrarySound(ctx, active, "library/monsoon.m4a")
			if err != nil {
				t.Fatalf("first use: %v", err)
			}
			if !first.IsLibrarySound() || *first.LibraryID != active || first.Title != "Monsoon Groove" ||
				first.Artist != "Momentum Library" || first.Genre == nil || *first.Genre != "pop" ||
				first.DurationMs != 30000 || first.AudioKey != "library/monsoon.m4a" || first.Status != "ready" ||
				first.IsOriginal || first.LicenseType != "licensed" || first.SourceMediaID != nil ||
				first.SourceReelID != nil || first.CreatorUserID != nil {
				t.Fatalf("first use made %+v", first)
			}
			second, err := store.EnsureLibrarySound(ctx, active, "library/other.m4a")
			if err != nil || second.ID != first.ID || second.AudioKey != "library/monsoon.m4a" {
				t.Fatalf("second use: %+v, %v; want the first row unchanged", second, err)
			}
			if read, err := store.GetAudioTrack(ctx, first.ID); err != nil || read.LibraryID == nil || *read.LibraryID != active {
				t.Fatalf("GetAudioTrack: %+v, %v", read, err)
			}
			var public bool
			if err := store.db.QueryRow(ctx, `SELECT is_public FROM audio_tracks WHERE id = $1`, first.ID).Scan(&public); err != nil || !public {
				t.Fatalf("is_public = %v (%v), want true", public, err)
			}

			// Eight first uses at once converge on one row.
			raced := uuid.New()
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source) VALUES ($1, 'Raced', 1000, 'library/raced.m4a', 'licensed')`, raced)
			var wg sync.WaitGroup
			ids := make([]uuid.UUID, 8)
			for i := range ids {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					tr, err := store.EnsureLibrarySound(ctx, raced, "library/raced.m4a")
					if err != nil {
						t.Errorf("racing use %d: %v", i, err)
						return
					}
					ids[i] = tr.ID
				}(i)
			}
			wg.Wait()
			var rows int
			if err := store.db.QueryRow(ctx, `SELECT COUNT(*) FROM audio_tracks WHERE library_id = $1`, raced).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("%d rows (%v), want 1", rows, err)
			}
			for i, id := range ids {
				if id != ids[0] {
					t.Fatalf("use %d answered %s, use 0 %s", i, id, ids[0])
				}
			}

			// An inactive row is never materialised.
			if _, err := store.EnsureLibrarySound(ctx, inactive, "library/retired.m4a"); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("inactive row: %v, want pgx.ErrNoRows", err)
			}
			if got, err := store.GetAudioLibraryTrack(ctx, inactive); err != nil || got != nil {
				t.Fatalf("GetAudioLibraryTrack(inactive) = %+v, %v; want nothing", got, err)
			}
			// No genre: the empty default post-service's shape requires.
			if plain, err := store.EnsureLibrarySound(ctx, untagged, "library/plain.m4a"); err != nil || plain.LicenseType != "standard" {
				t.Fatalf("untagged row: %+v, %v", plain, err)
			}

			// The second library id of one track is refused by the index.
			if _, err := store.db.Exec(ctx, `INSERT INTO audio_tracks (title, library_id) VALUES ('dup', $1)`, active); err == nil {
				t.Fatal("a second audio_tracks row for one library id was accepted")
			}
		})
	}
}

func TestLibrarySoundListReadsItsUsesBack(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			store, exec := librarySchema(t, shape)
			ctx := context.Background()
			used, popular, fresh := uuid.New(), uuid.New(), uuid.New()
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source, usage_count, created_at)
				VALUES ($1, 'Used', 1000, 'library/used.m4a', 'licensed', 0, NOW() - INTERVAL '3 days'),
				       ($2, 'Popular', 1000, 'library/popular.m4a', 'licensed', 5, NOW() - INTERVAL '2 days'),
				       ($3, 'Fresh', 1000, 'library/fresh.m4a', 'licensed', 0, NOW())`, used, popular, fresh)

			track, err := store.EnsureLibrarySound(ctx, used, "library/used.m4a")
			if err != nil {
				t.Fatal(err)
			}
			// Seven posts attach it: post-service's counter, both columns.
			exec(`UPDATE audio_tracks SET use_count = 7, usage_count = 7 WHERE id = $1`, track.ID)

			list, err := store.GetTrendingAudioLibrary(ctx, nil, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 3 || list[0].ID != used || list[1].ID != popular || list[2].ID != fresh {
				t.Fatalf("order = %v, want used (7), popular (5), fresh (0)", libraryIDs(list))
			}
			if list[0].UsageCount != 7 || list[0].AudioTrackID == nil || *list[0].AudioTrackID != track.ID {
				t.Fatalf("used row = %+v, want usage 7 and its sound's id", list[0])
			}
			if list[1].UsageCount != 5 || list[1].AudioTrackID != nil {
				t.Fatalf("popular row = %+v, want its own count and no sound yet", list[1])
			}
			one, err := store.GetAudioLibraryTrack(ctx, used)
			if err != nil || one == nil || one.UsageCount != 7 || one.AudioTrackID == nil {
				t.Fatalf("GetAudioLibraryTrack = %+v, %v", one, err)
			}
			found, err := store.SearchAudioLibrary(ctx, "use", 10, 0)
			if err != nil || len(found) != 1 || found[0].ID != used || found[0].UsageCount != 7 {
				t.Fatalf("search = %+v, %v", found, err)
			}

			// A withdrawn sound is not joined: the row falls back to its own
			// counter and shows no sound.
			exec(`UPDATE audio_tracks SET status = 'deleted' WHERE id = $1`, track.ID)
			one, err = store.GetAudioLibraryTrack(ctx, used)
			if err != nil || one.AudioTrackID != nil || one.UsageCount != 0 {
				t.Fatalf("after withdrawal = %+v, %v", one, err)
			}

			// Deleting the library row outright clears the link (ON DELETE
			// SET NULL): the track then names no authority and is refused.
			exec(`DELETE FROM audio_library WHERE id = $1`, used)
			after, err := store.GetAudioTrack(ctx, track.ID)
			if err != nil || after.LibraryID != nil {
				t.Fatalf("after the library row is deleted: %+v, %v", after, err)
			}
		})
	}
}

func libraryIDs(list []AudioLibraryTrack) []uuid.UUID {
	out := make([]uuid.UUID, len(list))
	for i, l := range list {
		out[i] = l.ID
	}
	return out
}
