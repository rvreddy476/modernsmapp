package postpurge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// releasingMedia is a MediaDeleter that also releases a purged post's media
// claims (*service.Service does both).
type releasingMedia struct {
	fakeMedia
	released []uuid.UUID
	fail     bool
}

func (m *releasingMedia) ReleasePurgedPostClaims(_ context.Context, postID uuid.UUID) error {
	if m.fail {
		return errors.New("media-service down")
	}
	m.released = append(m.released, postID)
	return nil
}

// Every purged post has its cover claim released; posts the tick leaves
// alone (inside the restore window) keep theirs.
func TestTick_ReleasesThePurgedPostsMediaClaims(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	md := &releasingMedia{fakeMedia: fakeMedia{failFor: map[uuid.UUID]int{}}}
	old, recent := uuid.New(), uuid.New()
	oldAt, recentAt := now.Add(-31*24*time.Hour), now.Add(-time.Hour)
	st.deletedAt[old] = &oldAt
	st.deletedAt[recent] = &recentAt

	NewWorker(st, md, Config{After: 30 * 24 * time.Hour, Interval: time.Minute}, nil).
		WithClock(func() time.Time { return now }).Tick(context.Background())

	if len(md.released) != 1 || md.released[0] != old {
		t.Fatalf("released = %v, want only the purged post %s", md.released, old)
	}
}

// A failed release does not fail the purge: the media drain and
// media-claims repeat it.
func TestTick_AFailedClaimReleaseDoesNotFailThePurge(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	md := &releasingMedia{fakeMedia: fakeMedia{failFor: map[uuid.UUID]int{}}, fail: true}
	old := uuid.New()
	oldAt := now.Add(-31 * 24 * time.Hour)
	st.deletedAt[old] = &oldAt
	rep := NewWorker(st, md, Config{After: 30 * 24 * time.Hour, Interval: time.Minute}, nil).
		WithClock(func() time.Time { return now }).Tick(context.Background())
	if rep.Purged != 1 || rep.Failed != 0 {
		t.Fatalf("report %+v", rep)
	}
}
