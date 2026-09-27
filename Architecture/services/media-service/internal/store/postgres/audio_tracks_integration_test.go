//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Alternate audio tracks (migration 019) against live PostgreSQL:
//
//	POSTGRES_DSN=postgres://…/app_test go test -tags integration ./internal/store/postgres/ -run AudioTrack -v
//
// Skips when POSTGRES_DSN is unset; refuses any database whose name does not
// end in _test (requireScratchDatabase). Applies migration 019 itself when
// the table is missing (CREATE TABLE IF NOT EXISTS, so harmless otherwise).

func audioTrackStore(t *testing.T) (*MediaAssetStore, uuid.UUID) {
	t.Helper()
	pool := testPool(t)
	requireScratchDatabase(t, pool)
	ctx := context.Background()

	sql, err := os.ReadFile(filepath.Join("..", "..", "..", "database", "migrations", "019_media_audio_tracks.sql"))
	if err != nil {
		t.Fatalf("read migration 019: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply migration 019: %v", err)
	}

	mediaID, uploader := uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type,
		    file_size_bytes, storage_bucket, storage_key, processing_status, moderation_status,
		    created_at, updated_at)
		VALUES ($1, $2, 'video', 'general', 'video/mp4', 100, 'media', $3, 'ready', 'approved', NOW(), NOW())`,
		mediaID, uploader, "user/"+uploader.String()+"/"+mediaID.String()+"/original")
	if err != nil {
		t.Fatalf("seed media: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, mediaID)
	})
	return New(pool), mediaID
}

func TestAudioTrackClaimCompleteReleaseFencing(t *testing.T) {
	store, mediaID := audioTrackStore(t)
	ctx := context.Background()
	src := "user/x/y/dub/hi/source"
	track := &MediaAudioTrack{MediaAssetID: mediaID, Language: "hi", Label: "Hindi", Source: "uploaded", SourceKey: &src}
	if _, err := store.ReplaceMediaAudioTrack(ctx, track); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if track.ID == uuid.Nil || track.Status != "pending" {
		t.Fatalf("inserted row: %+v", track)
	}

	claimed, err := store.ClaimMediaAudioTracks(ctx, 30*time.Minute, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var mine *MediaAudioTrack
	for i := range claimed {
		if claimed[i].ID == track.ID {
			mine = &claimed[i]
		}
	}
	if mine == nil || mine.Status != "processing" || mine.Attempts != 1 || mine.ClaimToken == nil {
		t.Fatalf("claimed row: %+v", mine)
	}

	// A second claim in the same window does not hand the track out again.
	again, _ := store.ClaimMediaAudioTracks(ctx, 30*time.Minute, 10)
	for _, c := range again {
		if c.ID == track.ID {
			t.Fatalf("a processing track must not be re-claimed before it is stale")
		}
	}

	// A stale worker's token cannot finish the job.
	stale := "not-the-token"
	if err := store.CompleteMediaAudioTrack(ctx, track.ID, []string{"720p"}, &stale); !errors.Is(err, ErrAudioTrackClaimLost) {
		t.Errorf("complete with a stale token: want ErrAudioTrackClaimLost, got %v", err)
	}
	if err := store.FailMediaAudioTrack(ctx, track.ID, "x", &stale); !errors.Is(err, ErrAudioTrackClaimLost) {
		t.Errorf("fail with a stale token: want ErrAudioTrackClaimLost, got %v", err)
	}

	// Release, then reclaim: attempts advance, a fresh token is issued.
	if err := store.ReleaseMediaAudioTrack(ctx, track.ID, "transient", mine.ClaimToken); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, _ := store.GetMediaAudioTrack(ctx, mediaID, track.ID)
	if got.Status != "pending" || got.ClaimToken != nil || got.LastError == nil || *got.LastError != "transient" {
		t.Errorf("released row: %+v", got)
	}
	re, _ := store.ClaimMediaAudioTracks(ctx, 30*time.Minute, 10)
	var second *MediaAudioTrack
	for i := range re {
		if re[i].ID == track.ID {
			second = &re[i]
		}
	}
	if second == nil || second.Attempts != 2 || second.ClaimToken == nil || *second.ClaimToken == *mine.ClaimToken {
		t.Fatalf("re-claimed row: %+v", second)
	}
	// The old token is dead after the reclaim.
	if err := store.CompleteMediaAudioTrack(ctx, track.ID, []string{"720p"}, mine.ClaimToken); !errors.Is(err, ErrAudioTrackClaimLost) {
		t.Errorf("complete with the previous token: want ErrAudioTrackClaimLost, got %v", err)
	}
	if err := store.SetMediaAudioTrackSourceKey(ctx, track.ID, "k", second.ClaimToken); err != nil {
		t.Errorf("set source key under the live token: %v", err)
	}
	if err := store.CompleteMediaAudioTrack(ctx, track.ID, []string{"720p", "360p"}, second.ClaimToken); err != nil {
		t.Fatalf("complete: %v", err)
	}
	got, _ = store.GetMediaAudioTrack(ctx, mediaID, track.ID)
	if got.Status != "ready" || len(got.Rungs) != 2 || got.ClaimToken != nil || got.LastError != nil {
		t.Errorf("completed row: %+v", got)
	}

	// Requeue after a variant prune starts the mux over with the source kept.
	if err := store.RequeueMediaAudioTrack(ctx, track.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetMediaAudioTrack(ctx, mediaID, track.ID)
	if got.Status != "pending" || len(got.Rungs) != 0 || got.Attempts != 0 || got.SourceKey == nil {
		t.Errorf("requeued row: %+v", got)
	}
}

func TestAudioTrackOneLanguagePerAsset(t *testing.T) {
	store, mediaID := audioTrackStore(t)
	ctx := context.Background()

	first := &MediaAudioTrack{MediaAssetID: mediaID, Language: "ta", Label: "Tamil", Source: "uploaded"}
	if _, err := store.ReplaceMediaAudioTrack(ctx, first); err != nil {
		t.Fatal(err)
	}
	// The constraint itself, not just the store's replace path.
	_, err := store.db.Exec(ctx, `
		INSERT INTO media_audio_tracks (media_asset_id, language, label, source)
		VALUES ($1, 'ta', 'Tamil again', 'generated')`, mediaID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("duplicate (media, language) must violate the UNIQUE constraint, got %v", err)
	}

	// Replace: one row, a new id, the previous source key and dub variants reported stale.
	src := "user/u/m/dub/ta/source"
	_, err = store.db.Exec(ctx, `UPDATE media_audio_tracks SET source_key = $2 WHERE id = $1`, first.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertVariants(ctx, []MediaVariant{
		{MediaAssetID: mediaID, Name: "dub_ta_720p", Mime: "video/mp4", ObjectKey: "user/u/m/dub/ta/720p"},
		{MediaAssetID: mediaID, Name: "720p", Mime: "video/mp4", ObjectKey: "user/u/m/720p"},
	}); err != nil {
		t.Fatal(err)
	}
	second := &MediaAudioTrack{MediaAssetID: mediaID, Language: "ta", Label: "Tamil v2", Source: "uploaded", SourceKey: &src}
	stale, err := store.ReplaceMediaAudioTrack(ctx, second)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if second.ID == first.ID {
		t.Errorf("replace must insert a new row")
	}
	if len(stale) != 2 || !contains(stale, src) || !contains(stale, "user/u/m/dub/ta/720p") {
		t.Errorf("stale keys: %v", stale)
	}
	n, _ := store.CountMediaAudioTracks(ctx, mediaID)
	if n != 1 {
		t.Errorf("rows after replace: %d", n)
	}
	remaining, _ := store.GetVariants(ctx, mediaID)
	for _, v := range remaining {
		if v.Name == "dub_ta_720p" {
			t.Errorf("replace must drop the previous dub variants")
		}
	}
	if len(remaining) != 1 || remaining[0].Name != "720p" {
		t.Errorf("the video's own variants must survive: %+v", remaining)
	}

	// Delete returns every key the track owned.
	if err := store.InsertVariants(ctx, []MediaVariant{
		{MediaAssetID: mediaID, Name: "dub_ta_360p", Mime: "video/mp4", ObjectKey: "user/u/m/dub/ta/360p"},
	}); err != nil {
		t.Fatal(err)
	}
	keys, err := store.DeleteMediaAudioTrack(ctx, mediaID, second.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(keys) != 2 || !contains(keys, src) || !contains(keys, "user/u/m/dub/ta/360p") {
		t.Errorf("deleted keys: %v", keys)
	}
	if _, err := store.GetMediaAudioTrack(ctx, mediaID, second.ID); !errors.Is(err, ErrAudioTrackNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if _, err := store.DeleteMediaAudioTrack(ctx, mediaID, second.ID); !errors.Is(err, ErrAudioTrackNotFound) {
		t.Errorf("double delete: %v", err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
