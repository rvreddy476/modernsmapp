//go:build integration

package postgres

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
)

// The purge's sound SQL against live PostgreSQL, on both shapes of
// audio_tracks (soundSchema). Runs in CI only:
//
//	POSTGRES_DSN=postgres://…/media_copyright_it_test go test -tags integration ./internal/store/postgres/ -run SoundReclaim -v
func TestSoundReclaimKeysAndRetireOnBothShapes(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			pool := soundSchema(t, shape)
			ctx := context.Background()
			source, other := uuid.New(), uuid.New()
			audio := "audio/u/" + source.String() + "/audio.m4a"
			waveform := "audio/u/" + source.String() + "/waveform.json"
			shared := "audio/shared/waveform.json"
			if _, err := pool.Exec(ctx, `
				INSERT INTO audio_tracks (title, source_media_id, audio_key, waveform_key, status)
				VALUES ('a', $1, $2, $3, 'ready'),
				       ('b', $1, $2, $4, 'ready'),
				       ('c', $5, 'audio/u/other/audio.m4a', $4, 'ready'),
				       ('d', NULL, 'audio-library/x.m4a', NULL, 'ready')`,
				source, audio, waveform, shared, other); err != nil {
				t.Fatalf("seed: %v", err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()

			keys, err := collectSoundKeys(ctx, tx, soundObjectKeysSQL("= $1"), source)
			if err != nil {
				t.Fatalf("collect: %v", err)
			}
			sort.Strings(keys)
			want := []string{audio, waveform}
			sort.Strings(want)
			if len(keys) != 2 || keys[0] != want[0] || keys[1] != want[1] {
				t.Fatalf("keys = %v, want %v (the shared waveform must be spared)", keys, want)
			}

			if _, err := tx.Exec(ctx, soundRetireSQL("= $1"), source); err != nil {
				t.Fatalf("retire: %v", err)
			}
			var live, retired int
			if err := tx.QueryRow(ctx, `SELECT
				COUNT(*) FILTER (WHERE source_media_id = $1),
				COUNT(*) FILTER (WHERE status = 'deleted' AND audio_key IS NULL AND waveform_key IS NULL AND source_media_id IS NULL)
				FROM audio_tracks`, source).Scan(&live, &retired); err != nil {
				t.Fatal(err)
			}
			if live != 0 || retired != 2 {
				t.Fatalf("after retire: %d rows still on the source, %d retired (want 0, 2)", live, retired)
			}
			var untouched int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM audio_tracks WHERE status = 'ready' AND audio_key IS NOT NULL`).Scan(&untouched); err != nil {
				t.Fatal(err)
			}
			if untouched != 2 {
				t.Fatalf("other sounds touched: %d ready rows left, want 2", untouched)
			}
		})
	}
}
