package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The hashtag SSE stream announces only what the hashtag page lists for the
// connected viewer: public, effectively approved, live posts whose author
// the viewer may see (canViewPosts, fail-closed).

// fakeGraphCanByViewer answers /v1/internal/graph/can from "viewer|author"
// pairs, so a follower and a stranger can get different answers.
func fakeGraphCanByViewer(t *testing.T, allow map[string]bool, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req canRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		atomic.AddInt32(calls, 1)
		data := map[string]bool{}
		for _, id := range req.TargetIDs {
			data[id] = allow[req.ViewerID+"|"+id]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func liveApprovedPublic() *postgres.PostAccessState {
	return &postgres.PostAccessState{ReviewStatus: "approved", Visibility: "public"}
}

func streamGateService(graphURL string, state func(uuid.UUID) (*postgres.PostAccessState, error)) *Service {
	s := newGatedService(graphURL)
	s.hashtagStreamPostState = func(_ context.Context, id uuid.UUID) (*postgres.PostAccessState, error) {
		return state(id)
	}
	return s
}

func TestHashtagStream_PrivateAuthorNotPushedToStranger(t *testing.T) {
	author, follower, stranger, post := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCanByViewer(t, map[string]bool{follower.String() + "|" + author.String(): true}, &calls)
	defer srv.Close()
	s := streamGateService(srv.URL, func(uuid.UUID) (*postgres.PostAccessState, error) { return liveApprovedPublic(), nil })

	if s.HashtagStreamAllows(context.Background(), &stranger, post, author) {
		t.Fatal("a private author's post was pushed to a stranger")
	}
	if s.HashtagStreamAllows(context.Background(), nil, post, author) {
		t.Fatal("a private author's post was pushed to a signed-out viewer")
	}
	if !s.HashtagStreamAllows(context.Background(), &follower, post, author) {
		t.Fatal("an allowed follower did not get the post")
	}
	if !s.HashtagStreamAllows(context.Background(), &author, post, author) {
		t.Fatal("the author did not get their own post")
	}
}

func TestHashtagStream_UnlistableRowsAreNotPushed(t *testing.T) {
	author, viewer := uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCanByViewer(t, map[string]bool{viewer.String() + "|" + author.String(): true}, &calls)
	defer srv.Close()

	future := time.Now().Add(time.Hour)
	cases := map[string]*postgres.PostAccessState{
		"pending":        {ReviewStatus: "pending", Visibility: "public"},
		"flagged":        {ReviewStatus: "flagged", Visibility: "public"},
		"restricted":     {ReviewStatus: "approved", ActiveRestrictionCount: 1, Visibility: "public"},
		"followers-only": {ReviewStatus: "approved", Visibility: "followers"},
		"deleted":        {ReviewStatus: "approved", Visibility: "public", Deleted: true},
		"scheduled":      {ReviewStatus: "approved", Visibility: "public", PublishAt: &future},
		"missing":        nil,
	}
	for name, st := range cases {
		st := st
		s := streamGateService(srv.URL, func(uuid.UUID) (*postgres.PostAccessState, error) { return st, nil })
		if s.HashtagStreamAllows(context.Background(), &viewer, uuid.New(), author) {
			t.Errorf("%s post was pushed", name)
		}
		// Even the author is not told about an unlistable row: the page
		// does not list it for them either.
		if s.HashtagStreamAllows(context.Background(), &author, uuid.New(), author) {
			t.Errorf("%s post was pushed to its author", name)
		}
	}
}

func TestHashtagStream_GraphDownIsNotPushed(t *testing.T) {
	author, viewer, post := uuid.New(), uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	s := streamGateService(srv.URL, func(uuid.UUID) (*postgres.PostAccessState, error) { return liveApprovedPublic(), nil })

	if s.HashtagStreamAllows(context.Background(), &viewer, post, author) {
		t.Fatal("pushed while graph-service could not decide")
	}
	// Unreachable entirely, too.
	srv.Close()
	if s.HashtagStreamAllows(context.Background(), &viewer, post, author) {
		t.Fatal("pushed with graph-service unreachable")
	}
}

func TestHashtagStream_PostStateErrorIsNotPushedOrCached(t *testing.T) {
	author, viewer, post := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCanByViewer(t, map[string]bool{viewer.String() + "|" + author.String(): true}, &calls)
	defer srv.Close()
	fail := true
	s := streamGateService(srv.URL, func(uuid.UUID) (*postgres.PostAccessState, error) {
		if fail {
			return nil, errors.New("db down")
		}
		return liveApprovedPublic(), nil
	})
	if s.HashtagStreamAllows(context.Background(), &viewer, post, author) {
		t.Fatal("pushed without the post's state")
	}
	fail = false
	if !s.HashtagStreamAllows(context.Background(), &viewer, post, author) {
		t.Fatal("a read error was cached as a denial")
	}
}

// One row read per post serves every subscriber on the instance, and the
// author answer is reused per (viewer, author) by the privacy gate cache.
func TestHashtagStream_CachesRowStateAndAuthorAnswer(t *testing.T) {
	author, viewer := uuid.New(), uuid.New()
	var graphCalls int32
	srv := fakeGraphCanByViewer(t, map[string]bool{viewer.String() + "|" + author.String(): true}, &graphCalls)
	defer srv.Close()
	reads := map[uuid.UUID]int{}
	s := streamGateService(srv.URL, func(id uuid.UUID) (*postgres.PostAccessState, error) {
		reads[id]++
		return liveApprovedPublic(), nil
	})
	post := uuid.New()
	others := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	_ = s.HashtagStreamAllows(context.Background(), &viewer, post, author)
	for _, other := range others {
		_ = s.HashtagStreamAllows(context.Background(), &other, post, author)
	}
	if reads[post] != 1 {
		t.Fatalf("post state read %d times for %d subscribers, want 1", reads[post], 1+len(others))
	}
	// A second post by the same author: one more row read, no graph call
	// for the viewer already answered.
	before := atomic.LoadInt32(&graphCalls)
	if !s.HashtagStreamAllows(context.Background(), &viewer, uuid.New(), author) {
		t.Fatal("allowed viewer denied on the second post")
	}
	if got := atomic.LoadInt32(&graphCalls); got != before {
		t.Fatalf("graph called again for a cached (viewer, author): %d -> %d", before, got)
	}
}
