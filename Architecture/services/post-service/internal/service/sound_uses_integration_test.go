//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Trending sounds against a real Postgres (2026-10-24): the candidate SQL of
// GET /v1/internal/sound-uses clause by clause — the source reel, the
// content kinds, the window and every row-level visibility exclusion — and
// the count the service makes of what is left.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run SoundUsesIT -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).

func TestSoundUsesITCandidateRows(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	counted := f.reelAt(t, now.Add(-time.Hour), &f.sound)
	sameDay := f.reelAt(t, now.Add(-2*time.Hour), &f.sound)
	lastWeek := f.reelAt(t, now.Add(-9*24*time.Hour), &f.sound)
	longVideo := f.reelAt(t, now.Add(-3*time.Hour), &f.sound)
	f.set(t, `UPDATE posts SET content_type = 'long_video' WHERE id = $1`, longVideo)

	excluded := map[string]uuid.UUID{}
	for name, mutate := range map[string]string{
		"private":       `UPDATE posts SET visibility = 'private' WHERE id = $1`,
		"followers":     `UPDATE posts SET visibility = 'followers' WHERE id = $1`,
		"pending":       `UPDATE posts SET review_status = 'pending' WHERE id = $1`,
		"members-only":  `UPDATE posts SET tier_required_id = gen_random_uuid() WHERE id = $1`,
		"scheduled":     `UPDATE posts SET publish_at = NOW() + INTERVAL '1 day' WHERE id = $1`,
		"deleted":       `UPDATE posts SET deleted_at = NOW() WHERE id = $1`,
		"18+":           `UPDATE posts SET age_restricted = TRUE WHERE id = $1`,
		"feed post":     `UPDATE posts SET content_type = 'post' WHERE id = $1`,
		"out of window": `UPDATE posts SET created_at = NOW() - INTERVAL '15 days' WHERE id = $1`,
	} {
		id := f.reelAt(t, now.Add(-time.Hour), &f.sound)
		f.set(t, mutate, id)
		excluded[name] = id
	}
	held := f.reelAt(t, now.Add(-time.Hour), &f.sound)
	f.restrict(t, held)
	excluded["held/restricted"] = held

	// The sound's own source reel: the post it names (f.post) playing it,
	// and another post carrying the source video.
	f.set(t, `UPDATE posts SET audio_track_id = $1 WHERE id = $2`, f.sound, f.post)
	excluded["source reel"] = f.post
	carrier := f.reelAt(t, now.Add(-time.Hour), &f.sound)
	f.set(t, `INSERT INTO post_media (post_id, media_id, kind, position) VALUES ($1, $2, 'video', 1)`, carrier, f.anonMediaFixture.media)
	excluded["carries the source video"] = carrier

	rows, next, err := f.store.SoundUseRows(ctx, now.Add(-14*24*time.Hour), now.Add(time.Minute), uuid.Nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]bool{}
	for _, r := range rows {
		if r.SoundID == f.sound {
			got[r.PostID] = true
		}
	}
	for _, id := range []uuid.UUID{counted, sameDay, lastWeek, longVideo} {
		if !got[id] {
			t.Errorf("use %s missing from the candidates", id)
		}
	}
	for name, id := range excluded {
		if got[id] {
			t.Errorf("%s post %s is a candidate", name, id)
		}
	}
	if next != nil {
		t.Errorf("next = %v on a page shorter than the limit", next)
	}

	// The service's count of those rows: the stranger posted three times
	// today (one use) and once nine days ago.
	f.svc.now = func() time.Time { return now.Add(time.Minute) }
	page, err := f.svc.SoundUses(ctx, uuid.Nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var daily []int
	for _, s := range page.Sounds {
		if s.AudioTrackID == f.sound {
			daily = s.DailyUses
		}
	}
	if daily == nil || daily[0] != 1 || daily[9] != 1 {
		t.Fatalf("daily uses = %v, want 1 today and 1 nine days ago", daily)
	}
}

// A page is whole sounds: a sound whose only candidate is its source reel
// still moves the cursor, and a full page names the next one.
func TestSoundUsesITPaging(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	f.reelAt(t, now.Add(-time.Hour), &f.sound)
	f.reelAt(t, now.Add(-time.Hour), &f.unnamed)

	first, next, err := f.store.SoundUseRows(ctx, now.Add(-14*24*time.Hour), now.Add(time.Minute), uuid.Nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || len(first) == 0 || first[0].SoundID != *next {
		t.Fatalf("first page %+v next %v", first, next)
	}
	second, _, err := f.store.SoundUseRows(ctx, now.Add(-14*24*time.Hour), now.Add(time.Minute), *next, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range second {
		if r.SoundID == first[0].SoundID {
			t.Fatalf("sound %s appears on two pages", r.SoundID)
		}
	}
}
