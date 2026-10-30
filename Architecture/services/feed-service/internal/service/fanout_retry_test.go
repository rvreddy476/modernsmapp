package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Audit M6: repost, Q&A and close-friends fan-outs retry on graph errors
// instead of committing the event with followers missing.

func TestFanoutRepost_GraphFailureIsRetried(t *testing.T) {
	s, _ := fanoutStubs(t, nil, nil, nil, false)
	s.graphURL = "http://127.0.0.1:1"
	if err := s.FanoutRepost(context.Background(), uuid.New(), uuid.New(), uuid.New(), time.Now(), "public"); err == nil {
		t.Fatal("an unreachable graph-service was swallowed; the reposter's followers never get the repost")
	}
}

func TestFanoutRepost_ReachesFollowersAndConnections(t *testing.T) {
	follower, friend := uuid.New(), uuid.New()
	s, tl := fanoutStubs(t, []uuid.UUID{follower}, []uuid.UUID{friend}, nil, false)
	reposter := uuid.New()
	if err := s.FanoutRepost(context.Background(), uuid.New(), uuid.New(), reposter, time.Now(), "public"); err != nil {
		t.Fatal(err)
	}
	for who, id := range map[string]uuid.UUID{"reposter": reposter, "follower": follower, "connection": friend} {
		if tl.rows(id) != 1 {
			t.Fatalf("%s got %d repost rows, want 1", who, tl.rows(id))
		}
	}
}

func TestFanoutQuestion_GraphFailureIsRetried(t *testing.T) {
	s, _ := fanoutStubs(t, nil, nil, nil, false)
	s.graphURL = "http://127.0.0.1:1"
	if err := s.FanoutQuestion(context.Background(), uuid.New(), uuid.New(), time.Now()); err == nil {
		t.Fatal("an unreachable graph-service was swallowed; followers never get the question")
	}
}

type erroringCelebs struct{}

func (erroringCelebs) IsCeleb(context.Context, uuid.UUID) (bool, error) {
	return false, errors.New("pg: connection reset")
}

func TestFanoutQuestion_CelebFlagErrorIsRetried(t *testing.T) {
	follower := uuid.New()
	s, tl := fanoutStubs(t, []uuid.UUID{follower}, nil, nil, false)
	s.celebs = erroringCelebs{}
	if err := s.FanoutQuestion(context.Background(), uuid.New(), uuid.New(), time.Now()); err == nil {
		t.Fatal("an unreadable celeb flag was treated as 'not celeb'")
	}
	if tl.rows(follower) != 0 {
		t.Fatal("a possibly-celeb author's question was pushed to followers")
	}
}

func TestFanoutPost_TrustedCloseFriendsFailureIsRetried(t *testing.T) {
	user := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"tc_close_friends_posts": true}})
	}))
	defer user.Close()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "close-friends") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer graph.Close()
	tl := newRecordingTimelines()
	s := &Service{
		graphURL: graph.URL, graphClient: &http.Client{Timeout: 2 * time.Second},
		userServiceURL: user.URL, userClient: &http.Client{Timeout: 2 * time.Second},
		timelines: tl, celebs: fixedCelebs(false),
	}
	if err := s.FanoutPost(context.Background(), uuid.New(), uuid.New(), time.Now(), "post", "trusted", uuid.Nil); err == nil {
		t.Fatal("a failed close-friends fetch was swallowed; the circle never gets the post")
	}
}

// Audit M1: undoing a repost removes its rows and their cached copies.
type fakeRepostRemover struct {
	owners []uuid.UUID
	err    error
	called struct{ original, reposter uuid.UUID }
}

func (f *fakeRepostRemover) DeleteRepostFromTimelines(_ context.Context, original, reposter uuid.UUID) ([]uuid.UUID, error) {
	f.called.original, f.called.reposter = original, reposter
	return f.owners, f.err
}

func TestUndoRepostFanout_RemovesRowsAndCachedCopies(t *testing.T) {
	mr := miniredis.RunT(t)
	owner1, owner2, bystander := uuid.New(), uuid.New(), uuid.New()
	original, reposter := uuid.New(), uuid.New()
	rm := &fakeRepostRemover{owners: []uuid.UUID{owner1, owner2}}
	s := &Service{rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()}), reposts: rm}
	for _, v := range []uuid.UUID{owner1, owner2, bystander} {
		_ = mr.Set(hydrationCacheKey(v, original), "{}")
	}
	if err := s.UndoRepostFanout(context.Background(), uuid.New(), original, reposter); err != nil {
		t.Fatal(err)
	}
	if rm.called.original != original || rm.called.reposter != reposter {
		t.Fatal("rows were looked up for the wrong post or reposter")
	}
	if mr.Exists(hydrationCacheKey(owner1, original)) || mr.Exists(hydrationCacheKey(owner2, original)) {
		t.Fatal("a removed repost row's cached copy survived")
	}
	if !mr.Exists(hydrationCacheKey(bystander, original)) {
		t.Fatal("an unrelated viewer's cache row was dropped")
	}
}

func TestUndoRepostFanout_StoreErrorIsRetried(t *testing.T) {
	s := &Service{reposts: &fakeRepostRemover{err: errors.New("scylla down")}}
	if err := s.UndoRepostFanout(context.Background(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("a failed repost removal was swallowed; the repost stays in followers' feeds")
	}
}
