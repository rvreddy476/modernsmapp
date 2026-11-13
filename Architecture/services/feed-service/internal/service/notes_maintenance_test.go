package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/google/uuid"
)

// notes_timeline rebuild (notes_maintenance.go, cmd/timeline-maintenance).

func TestNotesRowStale(t *testing.T) {
	owner, friend, stranger := uuid.New(), uuid.New(), uuid.New()
	follows := map[uuid.UUID]struct{}{friend: {}}
	live := postgres.NoteRow{TopLevel: true, Visibility: "public"}
	with := func(mod func(*postgres.NoteRow)) postgres.NoteRow {
		n := live
		mod(&n)
		return n
	}
	for _, tc := range []struct {
		name   string
		author uuid.UUID
		meta   postgres.NoteRow
		found  bool
		stale  bool
	}{
		{"followed author's live note", friend, live, true, false},
		{"own live note", owner, live, true, false},
		{"own followers-only note", owner, with(func(n *postgres.NoteRow) { n.Visibility = "private" }), true, false},
		{"followed author's followers note", friend, with(func(n *postgres.NoteRow) { n.Visibility = "followers" }), true, false},
		{"unknown to echo_notes", friend, postgres.NoteRow{}, false, true},
		{"deleted", friend, with(func(n *postgres.NoteRow) { n.Deleted = true }), true, true},
		{"own deleted note", owner, with(func(n *postgres.NoteRow) { n.Deleted = true }), true, true},
		{"reply", friend, with(func(n *postgres.NoteRow) { n.TopLevel = false }), true, true},
		{"unfollowed author", stranger, live, true, true},
		{"followed author's private note", friend, with(func(n *postgres.NoteRow) { n.Visibility = "private" }), true, true},
	} {
		if got := notesRowStale(owner, tc.author, tc.meta, tc.found, follows); got != tc.stale {
			t.Errorf("%s: stale=%v, want %v", tc.name, got, tc.stale)
		}
	}
}

func TestRebuildNotesTimeline(t *testing.T) {
	user, friend, big, gone := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g := &notesGraph{following: map[uuid.UUID][]uuid.UUID{user: {friend, big}}}
	now := time.Now()
	rec := func(meta *fakeNotesMeta, author uuid.UUID, age time.Duration, mod func(*postgres.NoteRow)) uuid.UUID {
		id := uuid.New()
		n := postgres.NoteRow{PostID: id, AuthorID: author, CreatedAt: now.Add(-age), TopLevel: true, Visibility: "public"}
		if mod != nil {
			mod(&n)
		}
		meta.notes[id] = n
		return id
	}
	setup := func(t *testing.T) (*Service, *fakeNotesTimelines, map[string]uuid.UUID) {
		s, tl, meta, _ := notesService(t, g, false)
		meta.pull[big] = 500000
		ids := map[string]uuid.UUID{
			"own":           rec(meta, user, time.Hour, nil),
			"friend":        rec(meta, friend, 2*time.Hour, nil),
			"friendMissing": rec(meta, friend, 3*time.Hour, nil), // never pushed
			"bigNote":       rec(meta, big, time.Hour, nil),
			"unfollowed":    rec(meta, gone, time.Hour, nil),
			"deleted":       rec(meta, friend, 4*time.Hour, func(n *postgres.NoteRow) { n.Deleted = true }),
		}
		for _, k := range []string{"own", "friend", "unfollowed", "deleted"} {
			n := meta.notes[ids[k]]
			_ = tl.AddToNotesTimeline(context.Background(), user, n.PostID, n.AuthorID, n.CreatedAt)
		}
		return s, tl, ids
	}
	has := func(tl *fakeNotesTimelines, id uuid.UUID) bool {
		tl.mu.Lock()
		defer tl.mu.Unlock()
		_, ok := tl.rows[user][id]
		return ok
	}

	t.Run("dry run changes nothing", func(t *testing.T) {
		s, tl, ids := setup(t)
		st, err := s.RebuildNotesTimeline(context.Background(), user, false)
		if err != nil {
			t.Fatal(err)
		}
		if st.Following != 2 || st.Pulled != 1 || st.Written != 3 || st.Stale != 2 || st.Scanned != 4 {
			t.Fatalf("stats %+v", st)
		}
		if has(tl, ids["friendMissing"]) || !has(tl, ids["unfollowed"]) || !has(tl, ids["deleted"]) {
			t.Fatal("a dry run wrote or deleted rows")
		}
	})
	t.Run("apply", func(t *testing.T) {
		s, tl, ids := setup(t)
		if _, err := s.RebuildNotesTimeline(context.Background(), user, true); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"own", "friend", "friendMissing"} {
			if !has(tl, ids[k]) {
				t.Errorf("%s note missing after the rebuild", k)
			}
		}
		for _, k := range []string{"unfollowed", "deleted", "bigNote"} {
			if has(tl, ids[k]) {
				t.Errorf("%s note still in the timeline after the rebuild", k)
			}
		}
	})
	t.Run("follow list unavailable: nothing changed", func(t *testing.T) {
		s, tl, ids := setup(t)
		down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer down.Close()
		s.graphURL = down.URL
		if _, err := s.RebuildNotesTimeline(context.Background(), user, true); err == nil {
			t.Fatal("rebuild ran without the follow list")
		}
		if !has(tl, ids["unfollowed"]) || has(tl, ids["friendMissing"]) {
			t.Fatal("rows changed although the follow list was unknown")
		}
	})
}
