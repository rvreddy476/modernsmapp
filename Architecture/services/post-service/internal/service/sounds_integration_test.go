//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
)

// Original sounds against a real Postgres (2026-09-29): the projection that
// now carries audio_track_id / audio_start_ms, the link and its two
// counters, the listing SQL clause by clause, and `sound` decided by the
// real media-access decision rather than a fake of it. Migration 059 is
// itself under test.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run SoundsIT -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).

type soundsITFixture struct {
	*anonMediaFixture
	store    *postgres.Store
	stranger uuid.UUID
	// sound was taken from the fixture's reel (f.post over f.media) and
	// names it; unnamed is the same audio with no source post recorded.
	sound, unnamed uuid.UUID
	posts          []uuid.UUID
	media          []uuid.UUID
}

func newSoundsITFixture(t *testing.T) *soundsITFixture {
	t.Helper()
	f := &soundsITFixture{
		anonMediaFixture: newAnonMediaFixture(t),
		stranger:         uuid.New(), sound: uuid.New(), unnamed: uuid.New(),
	}
	f.store = postgres.New(f.pool)
	f.svc.soundCounts = func(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error) {
		return map[uuid.UUID]*scylla.Counts{}, nil
	}
	for _, ddl := range []string{
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INTEGER`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS hls_master_key TEXT`,
	} {
		f.set(t, ddl)
	}
	f.set(t, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, f.stranger)
	f.set(t, `UPDATE posts SET content_type = 'flick', remix_setting = 'allow' WHERE id = $1`, f.post)
	f.set(t, `UPDATE media_assets SET duration_ms = 28400 WHERE id = $1`, f.media)
	f.set(t, `
		INSERT INTO audio_tracks (id, title, artist, duration_ms, source_media_id, source_reel_id, creator_user_id, status)
		VALUES ($1, 'Original sound - Asha', 'Asha', 28400, $2, $3, $4, 'ready')`, f.sound, f.anonMediaFixture.media, f.post, f.author)
	f.set(t, `
		INSERT INTO audio_tracks (id, title, duration_ms, source_media_id, status)
		VALUES ($1, 'Original sound', 28400, $2, 'ready')`, f.unnamed, f.anonMediaFixture.media)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = f.pool.Exec(bg, `UPDATE posts SET audio_track_id = NULL WHERE audio_track_id = ANY($1)`, []uuid.UUID{f.sound, f.unnamed})
		for _, id := range f.posts {
			if tx, err := f.pool.Begin(bg); err == nil {
				_, _ = tx.Exec(bg, `DELETE FROM post_restrictions WHERE post_id = $1`, id)
				_, _ = tx.Exec(bg, `UPDATE posts SET active_restriction_count = 0 WHERE id = $1`, id)
				_ = tx.Commit(bg)
			}
			for _, q := range []string{
				`DELETE FROM post_outbox_events WHERE aggregate_id = $1`,
				`DELETE FROM video_metadata WHERE post_id = $1`,
				`DELETE FROM post_media WHERE post_id = $1`,
				`DELETE FROM post_engagement_counts WHERE post_id = $1`,
				`DELETE FROM reel_drafts WHERE id = $1`,
				`DELETE FROM posts WHERE id = $1`,
			} {
				_, _ = f.pool.Exec(bg, q, id)
			}
		}
		_, _ = f.pool.Exec(bg, `DELETE FROM media_assets WHERE id = ANY($1)`, f.media)
		_, _ = f.pool.Exec(bg, `DELETE FROM audio_tracks WHERE id = ANY($1)`, []uuid.UUID{f.sound, f.unnamed})
		_, _ = f.pool.Exec(bg, `DELETE FROM users WHERE id = $1`, f.stranger)
	})
	return f
}

// video is a ready, passed video of `uploader`.
func (f *soundsITFixture) video(t *testing.T, uploader uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.media = append(f.media, id)
	f.set(t, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status, duration_ms)
		VALUES ($1, $2, 'video', 'ready', 'passed', 15000)`, id, uploader)
	return id
}

// reel is a public, approved, live reel of the stranger's over its own
// video, created `age` ago, that plays `sound` (nil: none).
func (f *soundsITFixture) reel(t *testing.T, age time.Duration, sound *uuid.UUID) uuid.UUID {
	t.Helper()
	return f.reelAt(t, time.Now().UTC().Add(-age).Truncate(time.Microsecond), sound)
}

func (f *soundsITFixture) reelAt(t *testing.T, at time.Time, sound *uuid.UUID) uuid.UUID {
	t.Helper()
	id, media := uuid.New(), f.video(t, f.stranger)
	f.posts = append(f.posts, id)
	f.set(t, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, audio_track_id, created_at, updated_at)
		VALUES ($1, $2, 'plays a sound', 'public', 'flick', 'approved', $3, $4, $4)`, id, f.stranger, sound, at)
	f.set(t, `INSERT INTO post_media (post_id, media_id, kind, position) VALUES ($1, $2, 'video', 0)`, id, media)
	return id
}

func (f *soundsITFixture) restrict(t *testing.T, postID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_restrictions (restriction_id, post_id, source, case_id, issuer, state, case_revision, last_decision_id, policy_version, reason_code)
		VALUES ($1, $2, 'copyright', $3, 'trust-safety-service', 'active', 1, $4, 'sound-test', 'test')`,
		uuid.New(), postID, uuid.New(), uuid.New()); err != nil {
		t.Fatalf("seed restriction: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET active_restriction_count = (SELECT COUNT(*) FROM post_restrictions WHERE post_id = $1 AND state = 'active') WHERE id = $1`, postID); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit restriction: %v", err)
	}
}

func (f *soundsITFixture) release(t *testing.T, postID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM post_restrictions WHERE post_id = $1`, postID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET active_restriction_count = 0 WHERE id = $1`, postID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// link reads what the posts row holds.
func (f *soundsITFixture) link(t *testing.T, post uuid.UUID) (*uuid.UUID, *int) {
	t.Helper()
	var sound *uuid.UUID
	var start *int
	if err := f.pool.QueryRow(context.Background(), `SELECT audio_track_id, audio_start_ms FROM posts WHERE id = $1`, post).Scan(&sound, &start); err != nil {
		t.Fatal(err)
	}
	return sound, start
}

// counters reads both counters of a sound.
func (f *soundsITFixture) counters(t *testing.T, sound uuid.UUID) (useCount, usageCount int) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(), `SELECT use_count, usage_count FROM audio_tracks WHERE id = $1`, sound).Scan(&useCount, &usageCount); err != nil {
		t.Fatal(err)
	}
	return
}

func (f *soundsITFixture) listed(t *testing.T, sound uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	rows, _, err := f.store.ListPostsBySound(context.Background(), sound, nil, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[uuid.UUID]bool{}
	for _, p := range rows {
		out[p.ID] = true
	}
	return out
}

func TestSoundsITMigration059(t *testing.T) {
	f := newSoundsITFixture(t)
	var def string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE tablename = 'posts' AND indexname = 'idx_posts_audio_track'`).Scan(&def); err != nil {
		t.Fatalf("idx_posts_audio_track: %v", err)
	}
	for _, part := range []string{"audio_track_id", "created_at DESC", "id DESC", "audio_track_id IS NOT NULL", "deleted_at IS NULL"} {
		if !strings.Contains(def, part) {
			t.Fatalf("index is %q, missing %q", def, part)
		}
	}
	for _, col := range []string{"usage_count", "source_reel_id", "use_count", "source_media_id"} {
		var n int
		if err := f.pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'audio_tracks' AND column_name = $1`, col).Scan(&n); err != nil || n == 0 {
			t.Fatalf("audio_tracks.%s: n=%d err=%v", col, n, err)
		}
	}
}

// Every read that scans a post carries the link; a post with no sound
// carries neither field, and a start the row never had reads 0.
func TestSoundsITPostReadsCarryTheLink(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	playing, silent, legacy := f.reel(t, time.Hour, &f.sound), f.reel(t, 2*time.Hour, nil), f.reel(t, 3*time.Hour, &f.sound)
	f.set(t, `UPDATE posts SET audio_start_ms = 1500 WHERE id = $1`, playing)
	f.set(t, `UPDATE posts SET audio_start_ms = 700 WHERE id = $1`, silent)
	f.set(t, `UPDATE posts SET audio_start_ms = NULL WHERE id = $1`, legacy)

	check := func(name string, p *postgres.Post) {
		t.Helper()
		switch p.ID {
		case playing:
			if p.AudioTrackID == nil || *p.AudioTrackID != f.sound || p.AudioStartMs == nil || *p.AudioStartMs != 1500 {
				t.Fatalf("%s: the reel that plays a sound read as %v / %v", name, p.AudioTrackID, p.AudioStartMs)
			}
		case silent:
			if p.AudioTrackID != nil || p.AudioStartMs != nil {
				t.Fatalf("%s: a reel with no sound read as %v / %v", name, p.AudioTrackID, p.AudioStartMs)
			}
		case legacy:
			if p.AudioTrackID == nil || p.AudioStartMs == nil || *p.AudioStartMs != 0 {
				t.Fatalf("%s: a sound with no start read as %v / %v", name, p.AudioTrackID, p.AudioStartMs)
			}
		}
	}
	for _, id := range []uuid.UUID{playing, silent, legacy} {
		p, err := f.store.GetPost(ctx, id)
		if err != nil || p == nil {
			t.Fatalf("GetPost: %v", err)
		}
		check("GetPost", p)
	}
	batch, err := f.store.GetPostsByIDs(ctx, []uuid.UUID{playing, silent, legacy})
	if err != nil || len(batch) != 3 {
		t.Fatalf("GetPostsByIDs: %d rows, %v", len(batch), err)
	}
	for i := range batch {
		check("GetPostsByIDs", &batch[i])
	}
	mine, _, err := f.store.GetPostsByAuthor(ctx, f.stranger, "", 50, "", true)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for i := range mine {
		if mine[i].ID == playing || mine[i].ID == silent || mine[i].ID == legacy {
			seen++
			check("GetPostsByAuthor", &mine[i])
		}
	}
	if seen != 3 {
		t.Fatalf("the author listing returned %d of the three reels", seen)
	}
}

func TestSoundsITAttachStoresTheStartAndCountsOnce(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	target := f.reel(t, time.Hour, nil)
	// The owner's counter ran ahead of ours before they were one.
	f.set(t, `UPDATE audio_tracks SET usage_count = 4, use_count = 1 WHERE id = $1`, f.sound)
	// Read, the count is the larger of the two, whichever service wrote it.
	if got, err := f.store.GetAudioTrack(ctx, f.sound); err != nil || got.UseCount != 4 {
		t.Fatalf("usage_count 4, use_count 1 read as %+v (%v), want 4", got, err)
	}
	f.set(t, `UPDATE audio_tracks SET usage_count = 1, use_count = 4 WHERE id = $1`, f.sound)
	if got, err := f.store.GetAudioTracksByIDs(ctx, []uuid.UUID{f.sound}); err != nil || got[f.sound] == nil || got[f.sound].UseCount != 4 {
		t.Fatalf("usage_count 1, use_count 4 read as %+v (%v), want 4", got[f.sound], err)
	}
	f.set(t, `UPDATE audio_tracks SET usage_count = 4, use_count = 1 WHERE id = $1`, f.sound)

	link, err := f.svc.AttachAudioToPost(ctx, f.stranger, target, f.sound, 12000)
	if err != nil || link == nil || link.StartMs != 12000 {
		t.Fatalf("link=%+v err=%v", link, err)
	}
	sound, start := f.link(t, target)
	if sound == nil || *sound != f.sound || start == nil || *start != 12000 {
		t.Fatalf("the row holds %v / %v", sound, start)
	}
	if use, usage := f.counters(t, f.sound); use != 5 || usage != 5 {
		t.Fatalf("use_count=%d usage_count=%d, want both 5", use, usage)
	}

	// The same sound again: the start moves, nothing is counted.
	if _, err := f.svc.AttachAudioToPost(ctx, f.stranger, target, f.sound, 99999); err != nil {
		t.Fatal(err)
	}
	if _, start := f.link(t, target); start == nil || *start != 0 {
		t.Fatalf("a start past the end was stored as %v, want 0", start)
	}
	if use, usage := f.counters(t, f.sound); use != 5 || usage != 5 {
		t.Fatalf("a retry was counted: use_count=%d usage_count=%d", use, usage)
	}
	if _, err := f.svc.AttachAudioToPost(ctx, f.stranger, target, f.sound, -5); err != nil {
		t.Fatal(err)
	}
	if _, start := f.link(t, target); start == nil || *start != 0 {
		t.Fatalf("a negative start was stored as %v", start)
	}

	// The flush worker's absolute write keeps both equal and never lowers.
	if err := f.store.SetAudioUseCount(ctx, f.sound, 9); err != nil {
		t.Fatal(err)
	}
	if use, usage := f.counters(t, f.sound); use != 9 || usage != 9 {
		t.Fatalf("after a flush of 9: use_count=%d usage_count=%d", use, usage)
	}
	if err := f.store.SetAudioUseCount(ctx, f.sound, 2); err != nil {
		t.Fatal(err)
	}
	if use, usage := f.counters(t, f.sound); use != 9 || usage != 9 {
		t.Fatalf("a flush of 2 lowered the count: use_count=%d usage_count=%d", use, usage)
	}
	if err := f.store.IncrementAudioUseCount(ctx, f.sound); err != nil {
		t.Fatal(err)
	}
	if use, usage := f.counters(t, f.sound); use != 10 || usage != 10 {
		t.Fatalf("after one more use: use_count=%d usage_count=%d", use, usage)
	}
	got, err := f.store.GetAudioTrack(ctx, f.sound)
	if err != nil || got.UseCount != 10 || got.SourcePostID == nil || *got.SourcePostID != f.post {
		t.Fatalf("sound read as %+v (%v)", got, err)
	}
}

func TestSoundsITAttachNeedsConsentAndSkipsTheOwnVideo(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	target := f.reel(t, time.Hour, nil)

	f.set(t, `UPDATE posts SET remix_setting = 'disallow' WHERE id = $1`, f.post)
	for name, sound := range map[string]uuid.UUID{"a sound that names its source": f.sound, "a sound that names none": f.unnamed} {
		if _, err := f.svc.AttachAudioToPost(ctx, f.stranger, target, sound, 0); !errors.Is(err, ErrSoundReuseNotAllowed) {
			t.Fatalf("%s, reuse turned off: got %v want %v", name, err, ErrSoundReuseNotAllowed)
		}
	}
	if sound, _ := f.link(t, target); sound != nil {
		t.Fatalf("a refused sound was linked: %v", sound)
	}
	if use, usage := f.counters(t, f.sound); use != 0 || usage != 0 {
		t.Fatalf("a refused use was counted: %d %d", use, usage)
	}
	// The source's author reuses their own sound on another of their reels.
	own := uuid.New()
	f.posts = append(f.posts, own)
	f.set(t, `INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at)
		VALUES ($1, $2, 'my second reel', 'public', 'flick', 'approved', NOW(), NOW())`, own, f.author)
	f.set(t, `INSERT INTO post_media (post_id, media_id, kind, position) VALUES ($1, $2, 'video', 0)`, own, f.video(t, f.author))
	if link, err := f.svc.AttachAudioToPost(ctx, f.author, own, f.sound, 0); err != nil || link == nil {
		t.Fatalf("the source's author: link=%v err=%v", link, err)
	}

	for _, setting := range []string{"allow", "allow_audio_only"} {
		f.set(t, `UPDATE posts SET remix_setting = $2, audio_track_id = NULL WHERE id = $1`, f.post, setting)
		f.set(t, `UPDATE posts SET audio_track_id = NULL WHERE id = $1`, target)
		if link, err := f.svc.AttachAudioToPost(ctx, f.stranger, target, f.sound, 0); err != nil || link == nil {
			t.Fatalf("remix_setting %s: link=%v err=%v", setting, link, err)
		}
	}

	// The reel's own video's sound is the audio it already plays.
	before, _ := f.counters(t, f.sound)
	for name, sound := range map[string]uuid.UUID{"named": f.sound, "by its media": f.unnamed} {
		link, err := f.svc.AttachAudioToPost(ctx, f.author, f.post, sound, 0)
		if err != nil || link != nil {
			t.Fatalf("own sound (%s): link=%v err=%v, want neither", name, link, err)
		}
	}
	if sound, _ := f.link(t, f.post); sound != nil {
		t.Fatalf("a reel was linked to its own sound: %v", sound)
	}
	if after, _ := f.counters(t, f.sound); after != before {
		t.Fatalf("an own sound was counted: %d -> %d", before, after)
	}
}

// soundOn runs attachSounds over the stored row for one viewer.
func (f *soundsITFixture) soundOn(t *testing.T, post uuid.UUID, viewer *uuid.UUID) *PostSound {
	t.Helper()
	p, err := f.store.GetPost(context.Background(), post)
	if err != nil || p == nil {
		t.Fatalf("GetPost: %v", err)
	}
	detail := &PostDetail{Post: p}
	f.svc.attachSounds(context.Background(), viewer, []*PostDetail{detail})
	if detail.AudioTrackID == nil {
		t.Fatal("the row lost audio_track_id")
	}
	return detail.Sound
}

// `sound` follows the SOURCE video's audience, by the real decision.
func TestSoundsITSoundFollowsTheSourceVideosAudience(t *testing.T) {
	f := newSoundsITFixture(t)
	playing := f.reel(t, time.Hour, &f.sound)
	f.set(t, `UPDATE posts SET audio_start_ms = 1500 WHERE id = $1`, playing)
	viewers := map[string]*uuid.UUID{"a signed-in viewer": &f.viewer, "the reel's author": &f.stranger, "signed out": nil}

	for name, v := range viewers {
		got := f.soundOn(t, playing, v)
		if got == nil || got.ID != f.sound || got.StartMs != 1500 || got.DurationMs != 28400 || got.Title != "Original sound - Asha" ||
			got.SourcePostID == nil || *got.SourcePostID != f.post || got.CreatorUserID == nil || *got.CreatorUserID != f.author {
			t.Fatalf("%s, source public: sound = %+v", name, got)
		}
	}

	for _, tc := range []struct {
		name         string
		apply, reset string
	}{
		{"made private", `UPDATE posts SET visibility = 'private' WHERE id = $1`, `UPDATE posts SET visibility = 'public' WHERE id = $1`},
		{"taken down", `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, `UPDATE posts SET deleted_at = NULL WHERE id = $1`},
		{"no longer approved", `UPDATE posts SET review_status = 'rejected' WHERE id = $1`, `UPDATE posts SET review_status = 'approved' WHERE id = $1`},
	} {
		f.set(t, tc.apply, f.post)
		for name, v := range viewers {
			if got := f.soundOn(t, playing, v); got != nil {
				t.Fatalf("source %s: %s still gets the sound: %+v", tc.name, name, got)
			}
		}
		f.set(t, tc.reset, f.post)
	}
	// Private, the source's own author still hears it.
	f.set(t, `UPDATE posts SET visibility = 'private' WHERE id = $1`, f.post)
	if got := f.soundOn(t, playing, &f.author); got == nil {
		t.Fatal("the source's author lost their own sound")
	}
	f.set(t, `UPDATE posts SET visibility = 'public' WHERE id = $1`, f.post)
	if got := f.soundOn(t, playing, nil); got == nil {
		t.Fatal("restored to public, the sound did not come back")
	}

	// A sound that is not ready is nobody's.
	f.set(t, `UPDATE audio_tracks SET status = 'processing' WHERE id = $1`, f.sound)
	if got := f.soundOn(t, playing, &f.viewer); got != nil {
		t.Fatalf("a sound still processing was attached: %+v", got)
	}
}

// One assertion per clause of the listing's WHERE.
func TestSoundsITBySoundListsOnlyPublicLiveReels(t *testing.T) {
	f := newSoundsITFixture(t)
	good := f.reel(t, time.Hour, &f.sound)
	legacy := f.reel(t, 2*time.Hour, &f.sound)
	f.set(t, `UPDATE posts SET content_type = 'reel' WHERE id = $1`, legacy)
	subject := f.reel(t, 3*time.Hour, &f.sound)

	if got := f.listed(t, f.sound); !got[good] || !got[legacy] || !got[subject] {
		t.Fatalf("a public, approved, live reel is missing: %v", got)
	}

	post := func(col string, val any) { f.set(t, `UPDATE posts SET `+col+` = $1 WHERE id = $2`, val, subject) }
	for _, tc := range []struct {
		name         string
		apply, reset func()
	}{
		{"unlisted", func() { post("visibility", "unlisted") }, func() { post("visibility", "public") }},
		{"private", func() { post("visibility", "private") }, func() { post("visibility", "public") }},
		{"followers only", func() { post("visibility", "followers") }, func() { post("visibility", "public") }},
		{"staged", func() { post("visibility", "staged") }, func() { post("visibility", "public") }},
		{"scheduled", func() { post("publish_at", time.Now().Add(24*time.Hour)) }, func() { post("publish_at", nil) }},
		{"deleted", func() { post("deleted_at", time.Now()) }, func() { post("deleted_at", nil) }},
		{"pending review", func() { post("review_status", "pending") }, func() { post("review_status", "approved") }},
		{"rejected", func() { post("review_status", "rejected") }, func() { post("review_status", "approved") }},
		{"held by a restriction", func() { f.restrict(t, subject) }, func() { f.release(t, subject) }},
		{"a long video", func() { post("content_type", "long_video") }, func() { post("content_type", "flick") }},
		{"a plain post", func() { post("content_type", "post") }, func() { post("content_type", "flick") }},
		{"another sound", func() { post("audio_track_id", f.unnamed) }, func() { post("audio_track_id", f.sound) }},
		{"no sound", func() { post("audio_track_id", nil) }, func() { post("audio_track_id", f.sound) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.apply()
			got := f.listed(t, f.sound)
			tc.reset()
			if got[subject] {
				t.Fatalf("a reel that is %s was listed", tc.name)
			}
			if !got[good] || !got[legacy] {
				t.Fatalf("%s removed a reel it should not have: %v", tc.name, got)
			}
		})
	}
	if got := f.listed(t, f.sound); !got[subject] {
		t.Fatal("restored, the reel did not come back")
	}
}

func TestSoundsITBySoundPagesByKeyset(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	// Two pairs share a created_at, so only the id can order them, and with
	// pages of two each pair is split across a page boundary: a cursor that
	// compared the time alone would lose the second of each.
	var ids []uuid.UUID
	for _, at := range []time.Time{base.Add(time.Minute), base, base, base.Add(-time.Minute), base.Add(-time.Minute)} {
		ids = append(ids, f.reelAt(t, at, &f.sound))
	}
	// The source reel plays its own sound here; the page shows it apart.
	f.set(t, `UPDATE posts SET audio_track_id = $2 WHERE id = $1`, f.post, f.sound)

	var walked []postgres.Post
	cursor := ""
	for page := 0; page < 10; page++ {
		rows, next, err := f.store.ListPostsBySound(ctx, f.sound, &f.post, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 2 {
			t.Fatalf("page %d holds %d rows", page, len(rows))
		}
		walked = append(walked, rows...)
		if next == "" {
			break
		}
		if !ValidSoundPostsCursor(next) {
			t.Fatalf("the store issued a cursor the route refuses: %q", next)
		}
		cursor = next
	}
	if len(walked) != len(ids) {
		t.Fatalf("walked %d rows, want %d", len(walked), len(ids))
	}
	seen := map[uuid.UUID]bool{}
	for i, p := range walked {
		if seen[p.ID] {
			t.Fatalf("row %s was listed twice", p.ID)
		}
		seen[p.ID] = true
		if p.ID == f.post {
			t.Fatal("the excluded source reel is a row")
		}
		if len(p.Media) != 1 {
			t.Fatalf("row %s carries %d media", p.ID, len(p.Media))
		}
		if i == 0 {
			continue
		}
		prev := walked[i-1]
		if p.CreatedAt.After(prev.CreatedAt) || (p.CreatedAt.Equal(prev.CreatedAt) && p.ID.String() > prev.ID.String()) {
			t.Fatalf("row %d (%s %s) is newer than row %d (%s %s)", i, p.CreatedAt, p.ID, i-1, prev.CreatedAt, prev.ID)
		}
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatalf("reel %s was never listed", id)
		}
	}

	// Without the exclusion the source is a row like any other.
	all, _, err := f.store.ListPostsBySound(ctx, f.sound, nil, 50, "")
	if err != nil || len(all) != len(ids)+1 {
		t.Fatalf("%d rows, %v; want %d", len(all), err, len(ids)+1)
	}
	if _, _, err := f.store.ListPostsBySound(ctx, f.sound, nil, 50, "not-a-cursor"); err == nil {
		t.Fatal("an unreadable cursor was read as the first page")
	}
}

func TestSoundsITPostsBySound(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	newer, older := f.reel(t, time.Hour, &f.sound), f.reel(t, 2*time.Hour, &f.sound)
	f.set(t, `UPDATE posts SET audio_start_ms = 1500 WHERE id = $1`, newer)

	for name, v := range map[string]*uuid.UUID{"a signed-in viewer": &f.viewer, "signed out": nil} {
		page, next, err := f.svc.PostsBySound(ctx, v, f.sound, 0, "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if page.Sound == nil || page.Sound.ID != f.sound || page.Sound.StartMs != 0 || next != "" {
			t.Fatalf("%s: sound=%+v next=%q", name, page.Sound, next)
		}
		if page.Origin == nil || page.Origin.ID != f.post || page.Origin.Sound != nil {
			t.Fatalf("%s: origin = %+v", name, page.Origin)
		}
		if len(page.Items) != 2 || page.Items[0].ID != newer || page.Items[1].ID != older {
			t.Fatalf("%s: items = %+v", name, page.Items)
		}
		if s := page.Items[0].Sound; s == nil || s.ID != f.sound || s.StartMs != 1500 {
			t.Fatalf("%s: the row's sound = %+v", name, s)
		}
		if m := page.Items[0].Media; len(m) != 1 || m[0].ProcessingStatus != "ready" || m[0].DurationMs != 15000 {
			t.Fatalf("%s: the row's media state = %+v", name, m)
		}
	}

	// The second page has no origin.
	first, next, err := f.svc.PostsBySound(ctx, &f.viewer, f.sound, 1, "")
	if err != nil || next == "" || first.Origin == nil || len(first.Items) != 1 {
		t.Fatalf("first page: %+v next=%q err=%v", first, next, err)
	}
	second, last, err := f.svc.PostsBySound(ctx, &f.viewer, f.sound, 1, next)
	if err != nil || last != "" || second.Origin != nil || len(second.Items) != 1 || second.Items[0].ID != older {
		t.Fatalf("second page: %+v next=%q err=%v", second, last, err)
	}
	if _, _, err := f.svc.PostsBySound(ctx, &f.viewer, f.sound, 1, "nope"); !errors.Is(err, ErrInvalidSoundCursor) {
		t.Fatalf("a bad cursor: got %v", err)
	}

	// The source goes private: the sound is nobody's but its author's, and
	// the refusal reads exactly like a sound that does not exist.
	_, _, missing := f.svc.PostsBySound(ctx, &f.viewer, uuid.New(), 0, "")
	if !errors.Is(missing, ErrSoundNotFound) {
		t.Fatalf("no such sound: got %v", missing)
	}
	f.set(t, `UPDATE posts SET visibility = 'private' WHERE id = $1`, f.post)
	for name, v := range map[string]*uuid.UUID{"a signed-in viewer": &f.viewer, "signed out": nil, "the author of a reel that plays it": &f.stranger} {
		_, _, err := f.svc.PostsBySound(ctx, v, f.sound, 0, "")
		if !errors.Is(err, ErrSoundNotFound) || err.Error() != missing.Error() {
			t.Fatalf("%s, source private: got %v, want exactly %v", name, err, missing)
		}
	}
	if page, _, err := f.svc.PostsBySound(ctx, &f.author, f.sound, 0, ""); err != nil || page.Origin == nil || len(page.Items) != 2 {
		t.Fatalf("the source's author: %+v %v", page, err)
	}
	f.set(t, `UPDATE posts SET visibility = 'public' WHERE id = $1`, f.post)

	// A hidden author's reel is no row (the listing's author gate).
	f.set(t, `INSERT INTO post_hidden_authors (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, f.stranger)
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM post_hidden_authors WHERE user_id = $1`, f.stranger)
	})
	page, _, err := f.svc.PostsBySound(ctx, &f.viewer, f.sound, 0, "")
	if err != nil || len(page.Items) != 0 || page.Origin == nil {
		t.Fatalf("hidden author: %+v %v", page, err)
	}
}

// "Use this sound" against the real rows, with media-service faked.
func TestSoundsITUseSound(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	maker := &fakeSoundMaker{}
	f.svc.soundMaker = maker

	got, err := f.svc.UseSound(ctx, f.viewer, f.post)
	if err != nil || got == nil {
		t.Fatalf("sound=%+v err=%v", got, err)
	}
	if len(maker.calls) != 1 || maker.calls[0].media != f.anonMediaFixture.media ||
		maker.calls[0].in.SourcePostID != f.post || maker.calls[0].in.CreatorUserID != f.author {
		t.Fatalf("media-service was asked %+v", maker.calls)
	}

	f.set(t, `UPDATE posts SET remix_setting = 'disallow' WHERE id = $1`, f.post)
	if _, err := f.svc.UseSound(ctx, f.viewer, f.post); !errors.Is(err, ErrSoundReuseNotAllowed) {
		t.Fatalf("reuse turned off, a stranger: got %v", err)
	}
	if _, err := f.svc.UseSound(ctx, f.author, f.post); err != nil {
		t.Fatalf("reuse turned off, the author: %v", err)
	}
	f.set(t, `UPDATE posts SET remix_setting = 'allow', visibility = 'private' WHERE id = $1`, f.post)
	if _, err := f.svc.UseSound(ctx, f.viewer, f.post); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("a private reel, a stranger: got %v", err)
	}
	f.set(t, `UPDATE posts SET visibility = 'public', content_type = 'long_video' WHERE id = $1`, f.post)
	var refusal *SoundRefusal
	if _, err := f.svc.UseSound(ctx, f.viewer, f.post); !errors.As(err, &refusal) || refusal.Code != SoundCodeNotAReel {
		t.Fatalf("a long video: got %v", err)
	}
	if len(maker.calls) != 2 {
		t.Fatalf("media-service was asked %d times, want 2 (the first use and the author's)", len(maker.calls))
	}

	// A reel that already plays the sound answers it, and nothing is made.
	playing := f.reel(t, time.Hour, &f.sound)
	f.set(t, `UPDATE posts SET content_type = 'flick' WHERE id = $1`, f.post)
	got, err = f.svc.UseSound(ctx, f.viewer, playing)
	if err != nil || got.ID != f.sound || got.StartMs != 0 || len(maker.calls) != 2 {
		t.Fatalf("sound=%+v err=%v calls=%d", got, err, len(maker.calls))
	}
}

// Both reel-draft publish paths used to drop the draft's sound.
func TestSoundsITReelDraftPublishKeepsTheSound(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	// CreatePost touches the cache after the insert; a client that cannot
	// connect fails those calls without reaching any Redis.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	svc := New(f.store, nil, rdb)
	svc.WithStoryAudience(NewStoryAudience(noRelationships{}))

	sound := f.sound.String()
	draft := func() *postgres.ReelDraft {
		media := f.video(t, f.stranger)
		d := &postgres.ReelDraft{AuthorID: f.stranger, MediaID: &media, Caption: "a reel with a sound", Visibility: "public",
			License: "standard", RemixSetting: "allow", CommentModeration: "none", CommentAccess: "everyone",
			LikesEnabled: true, CommentsEnabled: true, AllowEmbedding: true, PublishToFeed: true,
			AudioTrackID: &sound, AudioStartMs: 2500, OriginalAudioVol: 0.2, OverlayAudioVol: 1}
		if err := f.store.CreateDraft(ctx, d); err != nil {
			t.Fatal(err)
		}
		f.posts = append(f.posts, d.ID)
		return d
	}

	for name, publish := range map[string]func(d *postgres.ReelDraft) (*postgres.Post, error){
		"published now": func(d *postgres.ReelDraft) (*postgres.Post, error) {
			return svc.PublishDraft(ctx, d.ID, f.stranger, nil)
		},
		"published by the schedule worker": func(d *postgres.ReelDraft) (*postgres.Post, error) {
			return svc.publishClaimedReelDraft(ctx, d.ID, f.stranger)
		},
	} {
		before, _ := f.counters(t, f.sound)
		d := draft()
		post, err := publish(d)
		if err != nil || post == nil {
			t.Fatalf("%s: post=%v err=%v", name, post, err)
		}
		if post.AudioTrackID == nil || *post.AudioTrackID != f.sound || post.AudioStartMs == nil || *post.AudioStartMs != 2500 {
			t.Fatalf("%s: the answer carries %v / %v", name, post.AudioTrackID, post.AudioStartMs)
		}
		linked, start := f.link(t, post.ID)
		if linked == nil || *linked != f.sound || start == nil || *start != 2500 {
			t.Fatalf("%s: the row holds %v / %v", name, linked, start)
		}
		if after, usage := f.counters(t, f.sound); after != before+1 || usage != after {
			t.Fatalf("%s: use_count %d -> %d, usage_count %d", name, before, after, usage)
		}
	}
}

// A saved reel draft's sound can be removed: "" clears it, an absent field
// leaves it, a malformed id is refused, and a draft whose sound was removed
// publishes a reel that plays none.
func TestSoundsITDraftSoundRemoval(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	svc := New(f.store, nil, rdb)
	svc.WithStoryAudience(NewStoryAudience(noRelationships{}))

	media := f.video(t, f.stranger)
	d := &postgres.ReelDraft{AuthorID: f.stranger, MediaID: &media, Caption: "a reel", Visibility: "public",
		License: "standard", RemixSetting: "allow", CommentModeration: "none", CommentAccess: "everyone",
		LikesEnabled: true, CommentsEnabled: true, AllowEmbedding: true, PublishToFeed: true, OriginalAudioVol: 1, OverlayAudioVol: 1}
	if err := f.store.CreateDraft(ctx, d); err != nil {
		t.Fatal(err)
	}
	f.posts = append(f.posts, d.ID)
	stored := func() (*string, int) {
		t.Helper()
		var sound *string
		var start *int
		if err := f.pool.QueryRow(ctx, `SELECT audio_track_id, audio_start_ms FROM reel_drafts WHERE id = $1`, d.ID).Scan(&sound, &start); err != nil {
			t.Fatal(err)
		}
		if start == nil {
			return sound, -1
		}
		return sound, *start
	}
	str := func(v string) *string { return &v }
	num := func(v int) *int { return &v }

	// Picked and saved.
	got, err := svc.UpdateDraft(ctx, d.ID, f.stranger, &UpdateDraftInput{AudioTrackID: str(f.sound.String()), AudioStartMs: num(2500)})
	if err != nil {
		t.Fatal(err)
	}
	if sound, start := stored(); sound == nil || *sound != f.sound.String() || start != 2500 || got.AudioTrackID == nil || got.AudioStartMs != 2500 {
		t.Fatalf("after picking: row %v / %d, answer %v / %d", sound, start, got.AudioTrackID, got.AudioStartMs)
	}

	// A patch that does not mention the sound leaves it.
	if _, err := svc.UpdateDraft(ctx, d.ID, f.stranger, &UpdateDraftInput{Caption: str("a better caption")}); err != nil {
		t.Fatal(err)
	}
	if sound, start := stored(); sound == nil || *sound != f.sound.String() || start != 2500 {
		t.Fatalf("an absent field changed the sound: %v / %d", sound, start)
	}

	// A malformed id is refused and nothing of the patch is written.
	_, err = svc.UpdateDraft(ctx, d.ID, f.stranger, &UpdateDraftInput{Caption: str("never stored"), AudioTrackID: str("not-a-sound")})
	if !errors.Is(err, ErrInvalidDraftSound) {
		t.Fatalf("a malformed id: got %v want %v", err, ErrInvalidDraftSound)
	}
	var caption string
	if err := f.pool.QueryRow(ctx, `SELECT caption FROM reel_drafts WHERE id = $1`, d.ID).Scan(&caption); err != nil || caption != "a better caption" {
		t.Fatalf("a refused patch wrote its caption: %q %v", caption, err)
	}
	if sound, start := stored(); sound == nil || *sound != f.sound.String() || start != 2500 {
		t.Fatalf("a refused patch changed the sound: %v / %d", sound, start)
	}

	// Removed.
	got, err = svc.UpdateDraft(ctx, d.ID, f.stranger, &UpdateDraftInput{AudioTrackID: str("")})
	if err != nil {
		t.Fatal(err)
	}
	if sound, start := stored(); sound != nil || start != 0 {
		t.Fatalf("after removing: row %v / %d, want NULL / 0", sound, start)
	}
	if got.AudioTrackID != nil || got.AudioStartMs != 0 {
		t.Fatalf("after removing: the answer carries %v / %d", got.AudioTrackID, got.AudioStartMs)
	}

	// Published, the reel plays no added sound and no use is counted.
	before, _ := f.counters(t, f.sound)
	post, err := svc.PublishDraft(ctx, d.ID, f.stranger, nil)
	if err != nil || post == nil {
		t.Fatalf("publish: %v %v", post, err)
	}
	if linked, _ := f.link(t, post.ID); linked != nil || post.AudioTrackID != nil {
		t.Fatalf("the reel plays %v (answer %v) after its sound was removed", linked, post.AudioTrackID)
	}
	if after, _ := f.counters(t, f.sound); after != before {
		t.Fatalf("a removed sound was counted: %d -> %d", before, after)
	}
}

// The cached body (post:body:<id>) against a real Redis: it carries the
// link and never the sound, and making the link drops a body cached before
// it. Skipped unless M7_REDIS_ADDR names a Redis.
func TestSoundsITCachedBodyCarriesNoSound(t *testing.T) {
	addr := os.Getenv("M7_REDIS_ADDR")
	if addr == "" {
		t.Skip("M7_REDIS_ADDR not set")
	}
	f := newSoundsITFixture(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	svc := New(f.store, nil, rdb)
	svc.WithStoryAudience(NewStoryAudience(noRelationships{}))
	// Uses are counted in PostgreSQL here: the sharded counter would leave
	// keys for a flush worker of whatever stack owns this Redis.
	svc.audioCounter = nil

	playing, plain := f.reel(t, time.Hour, &f.sound), f.reel(t, 2*time.Hour, nil)
	f.set(t, `UPDATE posts SET audio_start_ms = 1500 WHERE id = $1`, playing)
	for _, id := range []uuid.UUID{playing, plain} {
		key := postCacheKey(id)
		t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })
	}
	cached := func(id uuid.UUID) map[string]json.RawMessage {
		t.Helper()
		raw, err := rdb.Get(ctx, postCacheKey(id)).Bytes()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	// Two reads: the first fills the cache, the second is served from it.
	for read := 0; read < 2; read++ {
		p, err := svc.getCachedPostBody(ctx, playing)
		if err != nil || p == nil {
			t.Fatalf("read %d: %v %v", read, p, err)
		}
		detail := &PostDetail{Post: p}
		svc.attachSounds(ctx, &f.viewer, []*PostDetail{detail})
		if detail.Sound == nil || detail.Sound.ID != f.sound || detail.Sound.StartMs != 1500 {
			t.Fatalf("read %d: sound = %+v", read, detail.Sound)
		}
		if p.AudioTrackID == nil || *p.AudioTrackID != f.sound || p.AudioStartMs == nil || *p.AudioStartMs != 1500 {
			t.Fatalf("read %d: the body carries %v / %v", read, p.AudioTrackID, p.AudioStartMs)
		}
		body := cached(playing)
		if body == nil {
			t.Fatalf("read %d: nothing was cached", read)
		}
		if _, ok := body["sound"]; ok {
			t.Fatalf("read %d: the cached body carries a sound: %v", read, body)
		}
		if string(body["audio_track_id"]) != `"`+f.sound.String()+`"` || string(body["audio_start_ms"]) != "1500" {
			t.Fatalf("read %d: the cached body lost the link: %s / %s", read, body["audio_track_id"], body["audio_start_ms"])
		}
	}

	// A body cached before the link is dropped when the link is made.
	if p, err := svc.getCachedPostBody(ctx, plain); err != nil || p == nil || p.AudioTrackID != nil {
		t.Fatalf("plain reel: %+v %v", p, err)
	}
	if cached(plain) == nil {
		t.Fatal("the plain reel was not cached")
	}
	if _, err := svc.AttachAudioToPost(ctx, f.stranger, plain, f.sound, 700); err != nil {
		t.Fatal(err)
	}
	if body := cached(plain); body != nil {
		t.Fatalf("the body cached before the link is still served: %v", body)
	}
	p, err := svc.getCachedPostBody(ctx, plain)
	if err != nil || p.AudioTrackID == nil || *p.AudioTrackID != f.sound || p.AudioStartMs == nil || *p.AudioStartMs != 700 {
		t.Fatalf("after the link: %+v %v", p, err)
	}
}
