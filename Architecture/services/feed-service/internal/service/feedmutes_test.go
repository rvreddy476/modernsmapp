package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Audit L1: /v1/feed/mute takes effect. L2: a repost of a muted original
// author is dropped. L8: mute input is validated and errors are not raw.

type mutingFeedbackStore struct {
	*fakeFeedbackStore
	users []uuid.UUID
	words []string
	err   error
	reads int
}

func (m *mutingFeedbackStore) ActiveFeedMutes(context.Context, uuid.UUID) ([]uuid.UUID, []string, error) {
	m.reads++
	return m.users, m.words, m.err
}

func emptyGraph(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": []string{}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFeedMute_UserMuteJoinsTheBlockSet(t *testing.T) {
	muted := uuid.New()
	srv := emptyGraph(t)
	store := &mutingFeedbackStore{fakeFeedbackStore: newFakeFeedbackStore(), users: []uuid.UUID{muted}}
	s := &Service{graphURL: srv.URL, graphClient: srv.Client(), feedback: store}
	set, err := s.resolveBlockedSet(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set[muted]; !ok {
		t.Fatal("an account muted through /v1/feed/mute is not filtered")
	}
}

func TestFeedMute_StoreFailureFailsClosed(t *testing.T) {
	srv := emptyGraph(t)
	store := &mutingFeedbackStore{fakeFeedbackStore: newFakeFeedbackStore(), err: errors.New("pg down")}
	s := &Service{graphURL: srv.URL, graphClient: srv.Client(), feedback: store}
	if _, err := s.resolveBlockedSet(context.Background(), uuid.New()); err == nil {
		t.Fatal("unreadable mutes produced an unfiltered block set")
	}
	viewer := uuid.New()
	s.kwCache = map[uuid.UUID]keywordCacheEntry{viewer: {keywords: []string{}, expires: time.Now().Add(time.Minute)}}
	if _, err := s.applyKeywordHideFilter(context.Background(), viewer, nil); err == nil {
		t.Fatal("unreadable mutes produced an unfiltered keyword pass")
	}
}

func TestFeedMute_HashtagMuteHidesPosts(t *testing.T) {
	store := &mutingFeedbackStore{fakeFeedbackStore: newFakeFeedbackStore(), words: []string{"#Spoilers"}}
	viewer := uuid.New()
	s := &Service{feedback: store, kwCache: map[uuid.UUID]keywordCacheEntry{viewer: {keywords: []string{}, expires: time.Now().Add(time.Minute)}}}
	posts := []HydratedPost{{ID: uuid.New(), Text: "big #spoilers inside"}, {ID: uuid.New(), Text: "nothing to see"}}
	got, err := s.applyKeywordHideFilter(WithRequestMemo(context.Background()), viewer, posts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "nothing to see" {
		t.Fatalf("hashtag mute left %d posts: %+v", len(got), got)
	}
}

func TestFeedMute_ReadOncePerRequest(t *testing.T) {
	store := &mutingFeedbackStore{fakeFeedbackStore: newFakeFeedbackStore()}
	s := &Service{feedback: store}
	ctx := WithRequestMemo(context.Background())
	viewer := uuid.New()
	for i := 0; i < 5; i++ {
		if _, err := s.feedMutes(ctx, viewer); err != nil {
			t.Fatal(err)
		}
	}
	if store.reads != 1 {
		t.Fatalf("mutes read %d times in one request, want 1", store.reads)
	}
}

func TestValidateMuteTarget(t *testing.T) {
	long := make([]byte, maxMuteWordLen+1)
	for i := range long {
		long[i] = 'a'
	}
	cases := []struct {
		typ, id string
		ok      bool
	}{
		{"user", uuid.New().String(), true},
		{"user", "not-a-uuid", false},
		{"topic", "cooking", true},
		{"hashtag", "#tag", true},
		{"hashtag", "#", false},
		{"topic", "  ", false},
		{"topic", string(long), false},
		{"channel", "x", false},
	}
	for _, tc := range cases {
		err := validateMuteTarget(tc.typ, tc.id)
		if (err == nil) != tc.ok {
			t.Errorf("validateMuteTarget(%q, %q) = %v, want ok=%v", tc.typ, tc.id, err, tc.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalidMute) {
			t.Errorf("refusal %v is not ErrInvalidMute", err)
		}
	}
	past := time.Now().Add(-time.Minute)
	if err := muteExpiry(&past, time.Now()); !errors.Is(err, ErrInvalidMute) {
		t.Error("a mute expiring in the past was accepted")
	}
}

func TestDropRepostsOfBlocked(t *testing.T) {
	muted, ok := uuid.New(), uuid.New()
	posts := []HydratedPost{
		{ID: uuid.New(), AuthorID: muted, IsRepost: true},  // repost of a muted author: goes
		{ID: uuid.New(), AuthorID: ok, IsRepost: true},     // repost of someone else: stays
		{ID: uuid.New(), AuthorID: muted, IsRepost: false}, // not a repost: the pre-window filter's job
	}
	got := dropRepostsOfBlocked(posts, map[uuid.UUID]struct{}{muted: {}})
	if len(got) != 2 || got[0].AuthorID != ok || got[1].IsRepost {
		t.Fatalf("got %+v", got)
	}
}

func TestApplyRepostOriginalAuthorFilter(t *testing.T) {
	muted := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": []string{muted.String()}})
	}))
	defer srv.Close()
	s := &Service{graphURL: srv.URL, graphClient: srv.Client()}
	posts := []HydratedPost{{ID: uuid.New(), AuthorID: muted, IsRepost: true}, {ID: uuid.New(), AuthorID: uuid.New()}}
	got, err := s.applyRepostOriginalAuthorFilter(context.Background(), uuid.New(), posts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AuthorID == muted {
		t.Fatal("a graph-muted original author came back through a repost")
	}
	// Fail closed when the set cannot be resolved.
	s.graphURL = "http://127.0.0.1:1"
	if _, err := s.applyRepostOriginalAuthorFilter(context.Background(), uuid.New(), []HydratedPost{{IsRepost: true}}); err == nil {
		t.Fatal("an unresolved block set served reposts unfiltered")
	}
	// A page with no repost never asks.
	if _, err := s.applyRepostOriginalAuthorFilter(context.Background(), uuid.New(), []HydratedPost{{ID: uuid.New()}}); err != nil {
		t.Fatalf("a page without reposts consulted the graph: %v", err)
	}
}
