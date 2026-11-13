package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeHashtagNotes struct {
	tags      []string
	ids       []uuid.UUID
	queried   bool
	gotTags   []string
	gotViewer uuid.UUID
	gotSince  time.Time
	gotLimit  int
}

func (f *fakeHashtagNotes) FollowedHashtagTags(context.Context, uuid.UUID) ([]string, error) {
	return f.tags, nil
}

func (f *fakeHashtagNotes) FollowedHashtagNoteIDs(_ context.Context, tags []string, viewer uuid.UUID, since time.Time, limit int) ([]uuid.UUID, error) {
	f.queried = true
	f.gotTags, f.gotViewer, f.gotSince, f.gotLimit = tags, viewer, since, limit
	return f.ids, nil
}

func TestFollowedHashtagNoteIDs_NoTagsNoQuery(t *testing.T) {
	st := &fakeHashtagNotes{}
	s := (&Service{}).WithFollowedHashtagNoteStore(st)
	ids, err := s.FollowedHashtagNoteIDs(context.Background(), uuid.New(), time.Now(), 50)
	if err != nil || ids == nil || len(ids) != 0 || st.queried {
		t.Fatalf("no follows: ids %v err %v queried %v", ids, err, st.queried)
	}
}

func TestFollowedHashtagNoteIDs_PassesTheViewerAndClamps(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	note := uuid.New()
	st := &fakeHashtagNotes{tags: []string{"cricket"}, ids: []uuid.UUID{note}}
	s := (&Service{now: func() time.Time { return now }}).WithFollowedHashtagNoteStore(st)
	viewer := uuid.New()

	ids, err := s.FollowedHashtagNoteIDs(context.Background(), viewer, time.Time{}, 10_000)
	if err != nil || len(ids) != 1 || ids[0] != note {
		t.Fatalf("ids %v err %v", ids, err)
	}
	if st.gotViewer != viewer || len(st.gotTags) != 1 || st.gotTags[0] != "cricket" {
		t.Fatalf("store asked for viewer %v tags %v", st.gotViewer, st.gotTags)
	}
	if st.gotLimit != FollowedHashtagNotesMaxLimit {
		t.Fatalf("limit %d, want the max %d", st.gotLimit, FollowedHashtagNotesMaxLimit)
	}
	if want := now.Add(-FollowedHashtagNotesMaxWindow); !st.gotSince.Equal(want) {
		t.Fatalf("since %v, want clamped to %v", st.gotSince, want)
	}

	recent := now.Add(-time.Hour)
	if _, err := s.FollowedHashtagNoteIDs(context.Background(), viewer, recent, 0); err != nil {
		t.Fatal(err)
	}
	if st.gotLimit != FollowedHashtagNotesDefaultLimit || !st.gotSince.Equal(recent) {
		t.Fatalf("defaults: limit %d since %v", st.gotLimit, st.gotSince)
	}
}
