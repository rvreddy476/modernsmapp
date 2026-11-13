package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/google/uuid"
)

// For You source 4: notes carrying a hashtag the viewer follows, through
// post-service's internal route, under the same filters as every other
// candidate.

func hashtagNotesServer(t *testing.T, viewer uuid.UUID, status int, ids []uuid.UUID) (*httptest.Server, *string) {
	t.Helper()
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/users/"+viewer.String()+"/followed-hashtag-notes" || r.URL.Query().Get("since") == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotKey = r.Header.Get("X-Internal-Service-Key")
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		out := []string{}
		for _, id := range ids {
			out = append(out, id.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"note_ids": out}})
	}))
	t.Cleanup(srv.Close)
	return srv, &gotKey
}

func forYouAll(t *testing.T, s *Service, viewer uuid.UUID) map[uuid.UUID]FeedItem {
	t.Helper()
	got := map[uuid.UUID]FeedItem{}
	cursor := ForYouCursor{}
	for page := 0; page < 10; page++ {
		items, next, err := s.GetNotesForYouPage(context.Background(), viewer, 5, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			got[it.PostID] = it
		}
		if next == nil {
			break
		}
		cursor = *next
	}
	return got
}

func TestNotesForYou_FollowedHashtagSourceUnderTheSameFilters(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_KEY", "feed-test-key")
	viewer, stranger, blocked := uuid.New(), uuid.New(), uuid.New()
	g := &notesGraph{blocked: []uuid.UUID{blocked}}
	s, _, meta, _ := notesService(t, g, false)
	old := time.Now().Add(-5 * 24 * time.Hour) // past the 48 h rising window
	rec := func(author uuid.UUID, mod func(*postgres.NoteRow)) uuid.UUID {
		id := uuid.New()
		n := postgres.NoteRow{PostID: id, AuthorID: author, CreatedAt: old, TopLevel: true, Visibility: "public"}
		if mod != nil {
			mod(&n)
		}
		meta.notes[id] = n
		return id
	}
	tagged := rec(stranger, nil)
	fromBlocked := rec(blocked, nil)
	own := rec(viewer, nil)
	deleted := rec(stranger, func(n *postgres.NoteRow) { n.Deleted = true })
	reply := rec(stranger, func(n *postgres.NoteRow) { n.TopLevel = false })
	private := rec(stranger, func(n *postgres.NoteRow) { n.Visibility = "followers" })
	unknown := uuid.New() // never recorded by feed-service

	srv, gotKey := hashtagNotesServer(t, viewer, 0, []uuid.UUID{tagged, fromBlocked, own, deleted, reply, private, unknown})
	s.postServiceURL, s.postClient = srv.URL, srv.Client()

	got := forYouAll(t, s, viewer)
	if it, ok := got[tagged]; !ok || it.Source != sourceEchoHashtag {
		t.Fatalf("followed-hashtag note missing or mislabelled: %+v (served %d)", it, len(got))
	}
	for name, id := range map[string]uuid.UUID{
		"blocked author": fromBlocked, "own note": own, "deleted note": deleted,
		"reply": reply, "non-public note": private, "unrecorded id": unknown,
	} {
		if _, ok := got[id]; ok {
			t.Errorf("For You served a %s from the hashtag source", name)
		}
	}
	if *gotKey != "feed-test-key" {
		t.Errorf("internal key not sent (got %q)", *gotKey)
	}
}

// Best effort: post-service failing leaves the other sources' page.
func TestNotesForYou_FollowedHashtagSourceFailureIsNotFatal(t *testing.T) {
	viewer, friend := uuid.New(), uuid.New()
	g := &notesGraph{following: map[uuid.UUID][]uuid.UUID{viewer: {friend}}}
	s, tl, meta, _ := notesService(t, g, false)
	following := seedNotes(tl, viewer, friend, 1, time.Now())[0]
	meta.notes[following] = postgres.NoteRow{PostID: following, AuthorID: friend, CreatedAt: time.Now(), TopLevel: true, Visibility: "public"}
	srv, _ := hashtagNotesServer(t, viewer, http.StatusInternalServerError, nil)
	s.postServiceURL, s.postClient = srv.URL, srv.Client()

	if _, ok := forYouAll(t, s, viewer)[following]; !ok {
		t.Fatal("a failing hashtag source took the Following note with it")
	}
}

func TestFetchFollowedHashtagNoteIDs_ErrorsAreNeverEmpty(t *testing.T) {
	viewer := uuid.New()
	for name, h := range map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError) // a decodable body: the status alone must fail it
			_, _ = w.Write([]byte(`{"data":{"note_ids":[]}}`))
		},
		"malformed": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) },
		"bad id": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"note_ids":["nope"]}}`))
		},
	} {
		srv := httptest.NewServer(h)
		s := &Service{postServiceURL: srv.URL, postClient: srv.Client()}
		if _, err := s.fetchFollowedHashtagNoteIDs(context.Background(), viewer, time.Now(), 10); err == nil || !strings.Contains(err.Error(), "followed-hashtag") {
			t.Errorf("%s: err %v, want an error", name, err)
		}
		srv.Close()
	}
}
