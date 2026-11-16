//go:build integration

package postgres

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Retired sounds (migration 032) against live PostgreSQL, on both shapes of
// audio_tracks (soundSchema): every path a sound's objects take. Runs in CI
// only:
//
//	POSTGRES_DSN=postgres://…/media_copyright_it_test go test -tags integration ./internal/store/postgres/ -run SoundRetire -v
func TestSoundRetireAndReclaimOnBothShapes(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			pool := soundSchema(t, shape)
			ctx := context.Background()
			applySoundMigration(t, pool)
			if _, err := pool.Exec(ctx, `
				CREATE TABLE media_assets (id UUID PRIMARY KEY, uploader_id UUID NOT NULL);
				CREATE TABLE media_blob_reclaim (object_key TEXT PRIMARY KEY, media_id UUID NOT NULL);
				CREATE TABLE IF NOT EXISTS posts (id UUID PRIMARY KEY, audio_track_id UUID);
				ALTER TABLE posts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
				CREATE TABLE post_media (post_id UUID, media_id UUID, kind TEXT, position INT);`); err != nil {
				t.Fatalf("support tables: %v", err)
			}

			author, other := uuid.New(), uuid.New()
			srcA, srcB, srcE, srcD := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			key := func(src uuid.UUID, f string) string { return "audio/" + src.String() + "/" + f }
			const shared = "audio/shared/waveform.json"
			if _, err := pool.Exec(ctx, `INSERT INTO media_assets VALUES ($1,$5),($2,$5),($3,$5),($4,$6)`,
				srcA, srcB, srcE, srcD, author, other); err != nil {
				t.Fatalf("seed assets: %v", err)
			}
			ids := map[string]uuid.UUID{}
			for _, s := range []struct {
				name, status, audio, wave string
				src                       uuid.UUID
			}{
				{"A", "ready", key(srcA, "audio.m4a"), shared, srcA},
				{"B", "processing", key(srcB, "audio.m4a"), key(srcB, "waveform.json"), srcB},
				{"E", "ready", key(srcE, "audio.m4a"), key(srcE, "waveform.json"), srcE},
				{"D", "ready", key(srcD, "audio.m4a"), shared, srcD},
			} {
				var id uuid.UUID
				if err := pool.QueryRow(ctx, `
					INSERT INTO audio_tracks (title, source_media_id, audio_key, waveform_key, status)
					VALUES ($1, $2, $3, $4, $5) RETURNING id`, s.name, s.src, s.audio, s.wave, s.status).Scan(&id); err != nil {
					t.Fatalf("seed %s: %v", s.name, err)
				}
				ids[s.name] = id
			}
			// The migration runs over the existing rows: their owner is recorded.
			applyMediaMigration(t, pool, "032_sound_retirement.sql")
			var owner uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT source_uploader_id FROM audio_tracks WHERE id = $1`, ids["D"]).Scan(&owner); err != nil || owner != other {
				t.Fatalf("backfill: D's owner %v (%v), want %v", owner, err, other)
			}

			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()

			// authorDelete is DeleteMedia / DeleteAssetForReferrer's sound
			// part: the unplayable sound's keys, its destruction, the retire,
			// then the asset row.
			authorDelete := func(src uuid.UUID) []string {
				t.Helper()
				keys, err := collectSoundKeys(ctx, tx, soundObjectKeysSQL(soundUnplayableOfSource("= $1")), src)
				if err != nil {
					t.Fatalf("collect: %v", err)
				}
				for _, q := range []string{soundDestroySQL(soundUnplayableOfSource("= $1")), soundRetireSQL("= $1"), `DELETE FROM media_assets WHERE id = $1`} {
					if _, err := tx.Exec(ctx, q, src); err != nil {
						t.Fatalf("author delete: %v", err)
					}
				}
				sort.Strings(keys)
				return keys
			}
			type row struct {
				status         string
				audio          *string
				source, former *uuid.UUID
				uploader       *uuid.UUID
				retired        *time.Time
			}
			read := func(id uuid.UUID) row {
				t.Helper()
				var r row
				if err := tx.QueryRow(ctx, `SELECT status, audio_key, source_media_id, former_source_media_id, source_uploader_id, retired_at
					FROM audio_tracks WHERE id = $1`, id).Scan(&r.status, &r.audio, &r.source, &r.former, &r.uploader, &r.retired); err != nil {
					t.Fatal(err)
				}
				return r
			}

			// 1. The author deletes A's video: A is retired and keeps its objects.
			if keys := authorDelete(srcA); len(keys) != 0 {
				t.Fatalf("a ready sound's objects were reclaimed with its video: %v", keys)
			}
			a := read(ids["A"])
			if a.status != SoundStatusRetired || a.audio == nil || *a.audio != key(srcA, "audio.m4a") || a.source != nil ||
				a.former == nil || *a.former != srcA || a.uploader == nil || *a.uploader != author || a.retired == nil {
				t.Fatalf("A after its video's delete: %+v", a)
			}

			// 2. The author deletes B's video: B never became playable and goes.
			if keys := authorDelete(srcB); len(keys) != 2 {
				t.Fatalf("B's keys = %v, want its audio and waveform", keys)
			}
			if b := read(ids["B"]); b.status != "deleted" || b.audio != nil {
				t.Fatalf("B after its video's delete: %+v", b)
			}

			// 3. A reel that plays A is heard through: a live post's first video.
			post, video := uuid.New(), uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO posts (id, audio_track_id) VALUES ($1, $2)`, post, ids["A"]); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO post_media VALUES ($1, $2, 'image', 0), ($1, $3, 'video', 1)`, post, uuid.New(), video); err != nil {
				t.Fatal(err)
			}
			var carrier uuid.UUID
			if err := tx.QueryRow(ctx, retiredSoundCarrierSQL, ids["A"], post).Scan(&carrier); err != nil || carrier != video {
				t.Fatalf("carrier = %v (%v), want the post's video %v", carrier, err, video)
			}
			if err := tx.QueryRow(ctx, retiredSoundCarrierSQL, ids["E"], post).Scan(&carrier); err != pgx.ErrNoRows {
				t.Fatalf("a post that does not play the sound carried it: %v", err)
			}

			// 4. The sweep keeps A while the post references it, even soft-deleted.
			sweep := func() []uuid.UUID {
				t.Helper()
				rows, err := tx.Query(ctx, unusedRetiredSoundsSQL, time.Now().Add(time.Hour), 10)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var out []uuid.UUID
				for rows.Next() {
					var id uuid.UUID
					if err := rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					out = append(out, id)
				}
				return out
			}
			if _, err := tx.Exec(ctx, `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, post); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(ctx, retiredSoundCarrierSQL, ids["A"], post).Scan(&carrier); err != pgx.ErrNoRows {
				t.Fatalf("a soft-deleted post still carries the sound: %v", err)
			}
			if got := sweep(); len(got) != 0 {
				t.Fatalf("sweep picked %v while a (restorable) post references it", got)
			}
			// Inside the grace nothing is picked either.
			if _, err := tx.Exec(ctx, `DELETE FROM posts WHERE id = $1`, post); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.Query(ctx, unusedRetiredSoundsSQL, time.Now().Add(-time.Hour), 10)
			if err != nil {
				t.Fatal(err)
			}
			graced := rows.Next()
			rows.Close()
			if graced {
				t.Fatal("sweep picked a sound retired inside the grace")
			}
			// Past it, A goes; the waveform D still names is spared.
			picked := sweep()
			if len(picked) != 1 || picked[0] != ids["A"] {
				t.Fatalf("sweep picked %v, want A", picked)
			}
			keys, err := destroySoundsTx(ctx, tx, unusedRetiredSoundMatch, picked)
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 1 || keys[0] != key(srcA, "audio.m4a") {
				t.Fatalf("sweep reclaimed %v, want only A's audio (the shared waveform is D's too)", keys)
			}
			if a := read(ids["A"]); a.status != "deleted" || a.audio != nil {
				t.Fatalf("A after the sweep: %+v", a)
			}

			// 5. The erasure takes the sound of a video the user deleted earlier.
			authorDelete(srcE)
			const owned = `SELECT id FROM media_assets WHERE uploader_id = $1`
			if _, err := tx.Exec(ctx, `INSERT INTO media_blob_reclaim (object_key, media_id) `+
				soundObjectKeysSQL(soundOfErasedUser(owned))+` ON CONFLICT (object_key) DO NOTHING`, author); err != nil {
				t.Fatalf("erasure reclaim: %v", err)
			}
			if _, err := tx.Exec(ctx, soundDestroySQL(soundOfErasedUser(owned)), author); err != nil {
				t.Fatalf("erasure destroy: %v", err)
			}
			if e := read(ids["E"]); e.status != "deleted" || e.audio != nil {
				t.Fatalf("E after the erasure: %+v", e)
			}
			var ledger uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT media_id FROM media_blob_reclaim WHERE object_key = $1`, key(srcE, "audio.m4a")).Scan(&ledger); err != nil || ledger != srcE {
				t.Fatalf("E's audio in the ledger as %v (%v), want its former source", ledger, err)
			}
			if d := read(ids["D"]); d.status != "ready" || d.audio == nil {
				t.Fatalf("another user's sound touched by the erasure: %+v", d)
			}

			// 6. A takedown destroys D, shared key and all (nobody else names it now).
			keys, err = destroySoundsTx(ctx, tx, soundTakedownMatch, ids["D"])
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(keys)
			if len(keys) != 2 || !strings.HasSuffix(keys[0], "audio.m4a") || keys[1] != shared {
				t.Fatalf("takedown reclaimed %v", keys)
			}
			if d := read(ids["D"]); d.status != "deleted" || d.audio != nil {
				t.Fatalf("D after the takedown: %+v", d)
			}
		})
	}
}
