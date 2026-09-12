package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// FanoutPost's subscriber leg: a long video with a channel reaches the
// channel's subscribers' home timelines, even when the author is a celeb
// and the follower leg is skipped (pull model), and a subscriber who is
// also a follower gets one row, not two.

// recordingTimelines is the timelineWriter FanoutPost writes to in these
// tests: it counts home-timeline rows per recipient. Workers write
// concurrently, hence the mutex.
type recordingTimelines struct {
	mu   sync.Mutex
	home map[uuid.UUID]int
}

func newRecordingTimelines() *recordingTimelines {
	return &recordingTimelines{home: map[uuid.UUID]int{}}
}

func (r *recordingTimelines) AddToAuthorTimeline(context.Context, uuid.UUID, uuid.UUID, time.Time, string) error {
	return nil
}

func (r *recordingTimelines) AddToHomeTimeline(_ context.Context, userID uuid.UUID, _, _ uuid.UUID, _ time.Time, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.home[userID]++
	return nil
}

func (r *recordingTimelines) rows(userID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.home[userID]
}

type fixedCelebs bool

func (c fixedCelebs) IsCeleb(context.Context, uuid.UUID) (bool, error) { return bool(c), nil }

// fanoutStubs is a graph-service stub (followers, connections) and a
// post-service stub (channel subscriber-ids) behind one Service.
func fanoutStubs(t *testing.T, followers, connections, subscribers []uuid.UUID, celeb bool) (*Service, *recordingTimelines) {
	t.Helper()
	encode := func(w http.ResponseWriter, ids []uuid.UUID) {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			out = append(out, id.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/followers/"):
			encode(w, followers)
		case strings.Contains(r.URL.Path, "/connections/"):
			encode(w, connections)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(graph.Close)
	post := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/subscriber-ids") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		out := make([]string, 0, len(subscribers))
		for _, id := range subscribers {
			out = append(out, id.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"subscriber_ids": out, "next_after": "", "has_more": false,
		}})
	}))
	t.Cleanup(post.Close)

	tl := newRecordingTimelines()
	s := &Service{
		graphURL:       graph.URL,
		graphClient:    &http.Client{Timeout: 2 * time.Second},
		postServiceURL: post.URL,
		postClient:     &http.Client{Timeout: 2 * time.Second},
		timelines:      tl,
		celebs:         fixedCelebs(celeb),
	}
	return s, tl
}

// A celeb's followers are served by the pull model, but a subscriber asked
// to see every upload and the Subscriptions tab reads the home timeline:
// the subscriber must hold the row.
func TestFanoutPost_CelebLongVideoReachesSubscribers(t *testing.T) {
	author, follower, subscriber, channel := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	s, tl := fanoutStubs(t, []uuid.UUID{follower}, nil, []uuid.UUID{subscriber}, true)

	if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "long_video", "public", channel); err != nil {
		t.Fatal(err)
	}
	if got := tl.rows(subscriber); got != 1 {
		t.Fatalf("subscriber of a celeb's channel got %d home rows, want 1", got)
	}
	if got := tl.rows(follower); got != 0 {
		t.Fatalf("a celeb's follower got %d pushed rows; the pull model must still apply to followers", got)
	}
	if got := tl.rows(author); got != 1 {
		t.Fatalf("author got %d own home rows, want 1", got)
	}
}

// A subscriber who also follows (post-service makes a subscribe a follow
// edge too) is already in the follower set: one row.
func TestFanoutPost_NonCelebSubscriberWhoFollowsGetsOneRow(t *testing.T) {
	author, both, onlySubscribed, onlyFollows, channel := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	s, tl := fanoutStubs(t, []uuid.UUID{both, onlyFollows}, nil, []uuid.UUID{both, onlySubscribed, both}, false)

	if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "long_video", "public", channel); err != nil {
		t.Fatal(err)
	}
	for who, id := range map[string]uuid.UUID{"follower+subscriber": both, "subscriber only": onlySubscribed, "follower only": onlyFollows} {
		if got := tl.rows(id); got != 1 {
			t.Errorf("%s got %d home rows, want exactly 1", who, got)
		}
	}
}

// The leg is for long videos only, and only when the event carried a
// channel: a reel from a channel owner is not an upload to subscribers,
// and uuid.Nil (no channel, or a restore) skips the leg outright.
func TestFanoutPost_SubscriberLegOnlyForLongVideosWithAChannel(t *testing.T) {
	author, subscriber, channel := uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		name        string
		contentType string
		channel     uuid.UUID
		wantRows    int
	}{
		{"reel with channel", "flick", channel, 0},
		{"long video without channel", "long_video", uuid.Nil, 0},
		{"legacy video spelling with channel", "video", channel, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, tl := fanoutStubs(t, nil, nil, []uuid.UUID{subscriber}, true)
			if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), tc.contentType, "public", tc.channel); err != nil {
				t.Fatal(err)
			}
			if got := tl.rows(subscriber); got != tc.wantRows {
				t.Fatalf("subscriber got %d rows, want %d", got, tc.wantRows)
			}
		})
	}
}

// A post-service failure on the subscriber leg is logged, not returned:
// the follower rows are already written and a Kafka redelivery would
// duplicate them.
func TestFanoutPost_SubscriberLegFailureDoesNotFailTheMessage(t *testing.T) {
	author, follower, channel := uuid.New(), uuid.New(), uuid.New()
	s, tl := fanoutStubs(t, []uuid.UUID{follower}, nil, nil, false)
	s.postServiceURL = "http://127.0.0.1:1"

	if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "long_video", "public", channel); err != nil {
		t.Fatalf("an unreachable post-service must not fail the fan-out: %v", err)
	}
	if got := tl.rows(follower); got != 1 {
		t.Fatalf("follower got %d rows, want 1", got)
	}
}
