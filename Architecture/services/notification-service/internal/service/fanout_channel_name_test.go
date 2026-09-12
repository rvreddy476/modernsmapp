package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A job enqueued from an event without channel_name resolves the name from
// post-service exactly once, with the internal key, and reuses it on retry.
func TestChannelNameFor_ResolvesOncePerJobAndCaches(t *testing.T) {
	author := uuid.New()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("internal key missing on %s", r.URL.Path)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/channels/"+author.String()) {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user_id":"` + author.String() + `","name":"Cal B","handle":"calb"}}`))
	}))
	defer srv.Close()

	f := newSubscriberFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{})
	f.SetEligibilityDeps("", srv.URL, "k")
	job := &postgres.FanoutJob{PostID: uuid.New(), AuthorID: author}

	if got := f.channelNameFor(context.Background(), job); got != "Cal B" {
		t.Fatalf("name = %q, want Cal B", got)
	}
	// Retry of the same job: served from the cache, no second round-trip.
	if got := f.channelNameFor(context.Background(), job); got != "Cal B" {
		t.Fatalf("cached name = %q", got)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("post-service hit %d times, want 1", n)
	}
}

// A lookup that cannot succeed must not block delivery: the name is copy,
// not a safety decision. Empty comes back and renderUpload falls back.
func TestChannelNameFor_FailureYieldsEmptyNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := newSubscriberFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{})
	f.SetEligibilityDeps("", srv.URL, "k")
	job := &postgres.FanoutJob{PostID: uuid.New(), AuthorID: uuid.New()}
	if got := f.channelNameFor(context.Background(), job); got != "" {
		t.Fatalf("name = %q, want empty on failure", got)
	}

	// No post-service wired at all (dev/partial deployment): same answer.
	bare := newSubscriberFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{})
	if got := bare.channelNameFor(context.Background(), job); got != "" {
		t.Fatalf("name without deps = %q, want empty", got)
	}
}
