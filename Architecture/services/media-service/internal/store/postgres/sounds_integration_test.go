//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// One sound per source video (migration 024) against live PostgreSQL:
//
//	POSTGRES_DSN=postgres://…/media_copyright_it_test go test -tags integration ./internal/store/postgres/ -run Sound -v
//
// Skips when POSTGRES_DSN is unset; refuses any database whose name does not
// end in _test (requireScratchDatabase). Everything happens in a schema of
// its own, created here and dropped at the end, so the two shapes of
// audio_tracks (whichever service migrated first) are both built from
// nothing and nothing of the database's own tables is touched.

// mediaFirstAudioTracks is audio_tracks as media-service migration 004
// leaves it on a database it reached first, followed by post-service's 013
// and 058 on top of it.
const mediaFirstAudioTracks = `
CREATE TABLE audio_tracks (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_media_id  UUID,
    source_reel_id   UUID,
    title            TEXT NOT NULL DEFAULT 'Original Sound',
    artist           TEXT NOT NULL DEFAULT '',
    genre            TEXT,
    audio_key        TEXT,
    waveform_key     TEXT,
    duration_ms      INT NOT NULL DEFAULT 0,
    sample_rate      INT,
    status           TEXT NOT NULL DEFAULT 'processing',
    is_original      BOOLEAN NOT NULL DEFAULT TRUE,
    license_type     TEXT NOT NULL DEFAULT 'standard',
    usage_count      INT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audio_tracks_source_media ON audio_tracks(source_media_id) WHERE source_media_id IS NOT NULL;
`

// postFirstAudioTracks is audio_tracks as post-service migration 013 creates
// it on a database IT reached first, then its 058, then the columns
// media-service 004 adds on top. posts and its foreign key are 013's.
const postFirstAudioTracks = `
CREATE TABLE audio_tracks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    artist TEXT NOT NULL DEFAULT '',
    duration_ms INT NOT NULL DEFAULT 0,
    media_id UUID NOT NULL,
    original_post_id UUID,
    genre TEXT NOT NULL DEFAULT '',
    is_original BOOLEAN NOT NULL DEFAULT true,
    use_count INT NOT NULL DEFAULT 0,
    is_trending BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    audio_track_id UUID REFERENCES audio_tracks(id)
);
ALTER TABLE audio_tracks
    ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS creator_user_id UUID;
ALTER TABLE audio_tracks ALTER COLUMN media_id DROP NOT NULL;

ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS source_media_id UUID;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS source_reel_id  UUID;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS audio_key       TEXT;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS waveform_key    TEXT;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS sample_rate     INT;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS status          TEXT NOT NULL DEFAULT 'processing';
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS license_type    TEXT NOT NULL DEFAULT 'standard';
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS usage_count     INT  NOT NULL DEFAULT 0;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX idx_audio_tracks_source_media ON audio_tracks(source_media_id) WHERE source_media_id IS NOT NULL;
`

// soundSchema builds one shape of audio_tracks in a schema of its own and
// returns a pool whose every connection resolves names there.
func soundSchema(t *testing.T, shape string) *pgxpool.Pool {
	t.Helper()
	requireScratchDatabase(t, testPool(t))
	ctx := context.Background()

	schema := "sounds_it_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	cfg, err := pgxpool.ParseConfig(os.Getenv("POSTGRES_DSN"))
	if err != nil {
		t.Fatal("parse POSTGRES_DSN")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connect with the schema's search_path")
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		pool.Close()
	})
	if _, err := pool.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(ctx, shape); err != nil {
		t.Fatalf("build audio_tracks: %v", err)
	}
	return pool
}

// applySoundMigration runs 024 the way the runner does: the whole file, in
// one transaction.
func applySoundMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	sql, err := os.ReadFile(filepath.Join("..", "..", "..", "database", "migrations", "024_sound_per_source.sql"))
	if err != nil {
		t.Fatalf("read migration 024: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("apply migration 024: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit migration 024: %v", err)
	}
}

func soundShapes() map[string]string {
	return map[string]string{"media-service first": mediaFirstAudioTracks, "post-service first": postFirstAudioTracks}
}

func newSound(source uuid.UUID, title string) *AudioTrack {
	post, creator, rate := uuid.New(), uuid.New(), 44_100
	waveform := "audio/u/" + source.String() + "/waveform.json"
	return &AudioTrack{
		SourceMediaID: &source, SourceReelID: &post, CreatorUserID: &creator,
		Title: title, Artist: "Asha", AudioKey: "audio/u/" + source.String() + "/audio.m4a",
		WaveformKey: &waveform, DurationMs: 11_900, SampleRate: &rate,
		Status: "ready", IsOriginal: true, LicenseType: "standard",
	}
}

// The migration on a table that already holds duplicates: the oldest row of
// each source is kept, whatever referenced a younger one points at it, the
// uses are carried over, and the index then refuses a second row.
func TestSoundMigrationKeepsTheOldestRowPerSource(t *testing.T) {
	for name, shape := range map[string]string{
		"media-service first, no posts table": mediaFirstAudioTracks,
		// posts.audio_track_id with no constraint: only the migration's own
		// look at the column re-points it.
		"media-service first, posts without a constraint": mediaFirstAudioTracks + `
			CREATE TABLE posts (id UUID PRIMARY KEY, audio_track_id UUID);`,
		"post-service first": postFirstAudioTracks,
	} {
		t.Run(name, func(t *testing.T) {
			pool := soundSchema(t, shape)
			ctx := context.Background()
			hasPosts := strings.Contains(shape, "CREATE TABLE posts")
			if _, err := pool.Exec(ctx, `
				CREATE TABLE sound_favourites (
				    user_id UUID NOT NULL,
				    sound_id UUID NOT NULL REFERENCES audio_tracks(id)
				)`); err != nil {
				t.Fatalf("create the second referrer: %v", err)
			}

			twice, once := uuid.New(), uuid.New()
			oldest, middle, youngest, single, sourceless := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			seed := func(id uuid.UUID, source *uuid.UUID, age time.Duration, uses int) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO audio_tracks (id, source_media_id, title, audio_key, status, usage_count, created_at)
					VALUES ($1, $2, 'Take', 'audio/k', 'ready', $3, NOW() - $4::interval)`,
					id, source, uses, fmt.Sprintf("%d seconds", int(age.Seconds()))); err != nil {
					t.Fatalf("seed sound: %v", err)
				}
			}
			seed(oldest, &twice, 3*time.Hour, 4)
			seed(middle, &twice, 2*time.Hour, 2)
			seed(youngest, &twice, time.Hour, 1)
			seed(single, &once, time.Hour, 9)
			seed(sourceless, nil, time.Hour, 0)
			seed(uuid.New(), nil, time.Hour, 0) // two rows with no source are not duplicates

			postOfYoungest, postOfOldest := uuid.New(), uuid.New()
			if hasPosts {
				if _, err := pool.Exec(ctx, `INSERT INTO posts (id, audio_track_id) VALUES ($1, $2), ($3, $4)`,
					postOfYoungest, youngest, postOfOldest, oldest); err != nil {
					t.Fatalf("seed posts: %v", err)
				}
			}
			fan := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO sound_favourites (user_id, sound_id) VALUES ($1, $2)`, fan, middle); err != nil {
				t.Fatalf("seed favourite: %v", err)
			}

			applySoundMigration(t, pool)
			// The runner applies a file once; a second run must still be
			// harmless, because a restore replays migrations.
			applySoundMigration(t, pool)

			var kept []uuid.UUID
			rows, err := pool.Query(ctx, `SELECT id FROM audio_tracks WHERE source_media_id = $1`, twice)
			if err != nil {
				t.Fatalf("list sounds: %v", err)
			}
			for rows.Next() {
				var id uuid.UUID
				_ = rows.Scan(&id)
				kept = append(kept, id)
			}
			rows.Close()
			if len(kept) != 1 || kept[0] != oldest {
				t.Fatalf("kept %v, want the oldest row %s alone", kept, oldest)
			}

			var usage, use, total int
			if err := pool.QueryRow(ctx, `SELECT usage_count, use_count FROM audio_tracks WHERE id = $1`, oldest).Scan(&usage, &use); err != nil {
				t.Fatalf("read counters: %v", err)
			}
			if usage != 7 || use != 7 {
				t.Errorf("the kept row counts usage %d use %d, want 7 and 7 (4 + 2 + 1)", usage, use)
			}
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM audio_tracks`).Scan(&total); err != nil || total != 4 {
				t.Errorf("%d rows left (%v), want 4: the kept one, the single one and two without a source", total, err)
			}

			if hasPosts {
				for post, want := range map[uuid.UUID]uuid.UUID{postOfYoungest: oldest, postOfOldest: oldest} {
					var got uuid.UUID
					if err := pool.QueryRow(ctx, `SELECT audio_track_id FROM posts WHERE id = $1`, post).Scan(&got); err != nil || got != want {
						t.Errorf("post %s plays %s (%v), want %s", post, got, err, want)
					}
				}
			}
			var favourite uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT sound_id FROM sound_favourites WHERE user_id = $1`, fan).Scan(&favourite); err != nil || favourite != oldest {
				t.Errorf("the favourite points at %s (%v), want %s", favourite, err, oldest)
			}

			_, err = pool.Exec(ctx, `INSERT INTO audio_tracks (source_media_id, title) VALUES ($1, 'Second')`, once)
			if err == nil || !strings.Contains(err.Error(), "uq_audio_tracks_source_media") {
				t.Errorf("a second sound of one source: %v, want the unique index to refuse it", err)
			}
		})
	}
}

// InsertSoundIfAbsent on both shapes: the first insert is kept, every later
// one is a no-op, and eight at once leave one row.
func TestSoundInsertIfAbsentKeepsOneRow(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			pool := soundSchema(t, shape)
			applySoundMigration(t, pool)
			store := New(pool)
			ctx := context.Background()

			source := uuid.New()
			first := newSound(source, "Take one")
			if inserted, err := store.InsertSoundIfAbsent(ctx, first); err != nil || !inserted {
				t.Fatalf("first insert: inserted %v, %v", inserted, err)
			}
			second := newSound(source, "Take two")
			if inserted, err := store.InsertSoundIfAbsent(ctx, second); err != nil || inserted {
				t.Fatalf("second insert: inserted %v, %v; want a no-op", inserted, err)
			}
			got, err := store.GetAudioTrackByMedia(ctx, source)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got.ID != first.ID || got.Title != "Take one" || got.AudioKey != first.AudioKey ||
				got.WaveformKey == nil || *got.WaveformKey != *first.WaveformKey ||
				got.SourceReelID == nil || *got.SourceReelID != *first.SourceReelID ||
				got.CreatorUserID == nil || *got.CreatorUserID != *first.CreatorUserID ||
				got.Status != "ready" || !got.IsOriginal || got.DurationMs != 11_900 {
				t.Errorf("read back %+v, want the first insert", got)
			}
			var public bool
			if err := pool.QueryRow(ctx, `SELECT is_public FROM audio_tracks WHERE id = $1`, first.ID).Scan(&public); err != nil || !public {
				t.Errorf("is_public = %v (%v), want true", public, err)
			}

			raced := uuid.New()
			var wg sync.WaitGroup
			var mu sync.Mutex
			winners := 0
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					inserted, err := store.InsertSoundIfAbsent(ctx, newSound(raced, fmt.Sprintf("Take %d", i)))
					if err != nil {
						t.Errorf("racing insert %d: %v", i, err)
					}
					if inserted {
						mu.Lock()
						winners++
						mu.Unlock()
					}
				}(i)
			}
			wg.Wait()
			var rows int
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM audio_tracks WHERE source_media_id = $1`, raced).Scan(&rows); err != nil {
				t.Fatalf("count: %v", err)
			}
			if winners != 1 || rows != 1 {
				t.Errorf("%d inserts kept over %d rows, want 1 and 1", winners, rows)
			}

			if _, err := store.InsertSoundIfAbsent(ctx, &AudioTrack{Title: "No source"}); err == nil {
				t.Error("a sound with no source was inserted")
			}
		})
	}
}

// FillSoundOrigin never overwrites, and the reads survive a row written in
// post-service's shape: no audio key, its own counter.
func TestSoundOriginAndCountersOnBothShapes(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			pool := soundSchema(t, shape)
			applySoundMigration(t, pool)
			store := New(pool)
			ctx := context.Background()

			source := uuid.New()
			sound := newSound(source, "Take")
			post, creator := *sound.SourceReelID, *sound.CreatorUserID
			sound.SourceReelID, sound.CreatorUserID = nil, nil
			if _, err := store.InsertSoundIfAbsent(ctx, sound); err != nil {
				t.Fatalf("insert: %v", err)
			}
			if err := store.FillSoundOrigin(ctx, sound.ID, &post, nil); err != nil {
				t.Fatalf("fill the post: %v", err)
			}
			otherPost, otherCreator := uuid.New(), uuid.New()
			if err := store.FillSoundOrigin(ctx, sound.ID, &otherPost, &creator); err != nil {
				t.Fatalf("fill the creator: %v", err)
			}
			if err := store.FillSoundOrigin(ctx, sound.ID, &otherPost, &otherCreator); err != nil {
				t.Fatalf("fill again: %v", err)
			}
			got, err := store.GetAudioTrack(ctx, sound.ID)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got.SourceReelID == nil || *got.SourceReelID != post || got.CreatorUserID == nil || *got.CreatorUserID != creator {
				t.Errorf("origin is post %v creator %v, want the first values %s and %s", got.SourceReelID, got.CreatorUserID, post, creator)
			}

			// The other way round: a creator is there and the post is not.
			// Titled apart from the rest, so the lists below do not hold it.
			owned := newSound(uuid.New(), "Owned")
			ownedCreator := *owned.CreatorUserID
			owned.SourceReelID = nil
			if _, err := store.InsertSoundIfAbsent(ctx, owned); err != nil {
				t.Fatalf("insert the owned sound: %v", err)
			}
			if err := store.FillSoundOrigin(ctx, owned.ID, &otherPost, &otherCreator); err != nil {
				t.Fatalf("fill the owned sound's post: %v", err)
			}
			got, err = store.GetAudioTrack(ctx, owned.ID)
			if err != nil {
				t.Fatalf("read the owned sound: %v", err)
			}
			if got.SourceReelID == nil || *got.SourceReelID != otherPost || got.CreatorUserID == nil || *got.CreatorUserID != ownedCreator {
				t.Errorf("owned sound: post %v creator %v, want the new post %s and its own creator %s", got.SourceReelID, got.CreatorUserID, otherPost, ownedCreator)
			}
			if _, err := pool.Exec(ctx, `UPDATE audio_tracks SET status = 'deleted' WHERE id = $1`, owned.ID); err != nil {
				t.Fatalf("retire the owned sound: %v", err)
			}

			// A row with no audio key and only post-service's counter.
			bare, bareSource := uuid.New(), uuid.New()
			if _, err := pool.Exec(ctx, `
				INSERT INTO audio_tracks (id, source_media_id, title, status, usage_count, use_count)
				VALUES ($1, $2, 'Bare take', 'ready', 1, 5)`, bare, bareSource); err != nil {
				t.Fatalf("seed bare row: %v", err)
			}
			row, err := store.GetAudioTrack(ctx, bare)
			if err != nil {
				t.Fatalf("read a row without an audio key: %v", err)
			}
			if row.AudioKey != "" || row.UsageCount != 1 || row.UseCount != 5 || row.WireUsageCount() != 5 {
				t.Errorf("bare row: key %q usage %d use %d wire %d", row.AudioKey, row.UsageCount, row.UseCount, row.WireUsageCount())
			}

			// The lists order by the counter the wire carries: the bare row
			// (1 and 5) is ahead of the sound (3 and 0).
			if _, err := pool.Exec(ctx, `UPDATE audio_tracks SET usage_count = 3 WHERE id = $1`, sound.ID); err != nil {
				t.Fatalf("seed the sound's uses: %v", err)
			}
			for _, list := range []struct {
				name string
				read func() ([]AudioTrack, error)
			}{
				{"trending", func() ([]AudioTrack, error) { return store.GetTrendingAudioTracks(ctx, 10, 0) }},
				{"search", func() ([]AudioTrack, error) { return store.SearchAudioTracks(ctx, "take", 10, 0) }},
			} {
				tracks, err := list.read()
				if err != nil {
					t.Fatalf("%s: %v", list.name, err)
				}
				if len(tracks) != 2 || tracks[0].ID != bare || tracks[1].ID != sound.ID {
					t.Errorf("%s lists %d rows, want the bare row (5 uses) then the sound (3)", list.name, len(tracks))
				}
			}

			if err := store.IncrementAudioUsageCount(ctx, bare); err != nil {
				t.Fatalf("count a use: %v", err)
			}
			row, _ = store.GetAudioTrack(ctx, bare)
			if row.UsageCount != 6 || row.UseCount != 6 {
				t.Errorf("after one use: usage %d use %d, want 6 and 6", row.UsageCount, row.UseCount)
			}
		})
	}
}
