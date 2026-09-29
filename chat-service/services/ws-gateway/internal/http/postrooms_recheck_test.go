package http

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// scriptedViewer answers per call so a test can turn the authority's
// answer over time (yes at join, no or undecided at re-check).
type scriptedViewer struct {
	decide func(postID string, call int) (bool, error)
	calls  int
	asked  []string
}

func (v *scriptedViewer) MayView(_ context.Context, _ string, postID string) (bool, error) {
	v.calls++
	v.asked = append(v.asked, postID)
	return v.decide(postID, v.calls)
}

func joinedServer(t *testing.T, viewer PostViewAuthorizer, posts ...string) (*Server, *postRoomGrants, uuid.UUID, time.Time) {
	t.Helper()
	user := uuid.New()
	t0 := time.Now()
	s := &Server{opts: ServerOptions{EnablePostRooms: true, PostViewer: viewer}}
	grants := newPostRoomGrants(postRoomGrantTTL)
	for _, p := range posts {
		if _, ok := s.mayJoinPostRoom(context.Background(), grants, user, p, t0); !ok {
			t.Fatalf("join of %s refused before the re-check even ran", p)
		}
	}
	return s, grants, user, t0
}

func TestPostRoomRecheckDropsSeatWhenAuthorityTurnsNo(t *testing.T) {
	post := uuid.NewString()
	viewer := &scriptedViewer{decide: func(_ string, call int) (bool, error) { return call == 1, nil }}
	s, grants, user, t0 := joinedServer(t, viewer, post)

	evicted := s.recheckPostRooms(context.Background(), grants, user, t0.Add(time.Minute), postRoomRecheckBatch)
	if len(evicted) != 1 || evicted[0].PostID != post || evicted[0].Reason != "denied" {
		t.Fatalf("the seat survived the authority's no: %+v", evicted)
	}
	if grants.held(post) || grants.fresh(post, t0.Add(time.Minute)) {
		t.Fatal("the cached grant outlived the eviction")
	}
}

func TestPostRoomRecheckKeepsSeatWhileAuthoritySaysYes(t *testing.T) {
	post := uuid.NewString()
	viewer := &scriptedViewer{decide: func(string, int) (bool, error) { return true, nil }}
	s, grants, user, t0 := joinedServer(t, viewer, post)

	at := t0.Add(4 * time.Minute)
	if evicted := s.recheckPostRooms(context.Background(), grants, user, at, postRoomRecheckBatch); len(evicted) != 0 {
		t.Fatalf("a still-visible post was evicted: %+v", evicted)
	}
	if viewer.calls != 2 {
		t.Fatalf("the re-check did not re-ask the authority (%d calls)", viewer.calls)
	}
	// A re-verified yes is as good as a fresh one: the grant is renewed.
	if !grants.fresh(post, at.Add(postRoomGrantTTL-time.Second)) {
		t.Fatal("a re-verified seat was not renewed")
	}
}

func TestPostRoomRecheckToleratesOneBlipAndDropsOnTheSecond(t *testing.T) {
	post := uuid.NewString()
	viewer := &scriptedViewer{decide: func(_ string, call int) (bool, error) {
		if call == 1 {
			return true, nil
		}
		return false, errors.New("post-service 503")
	}}
	s, grants, user, t0 := joinedServer(t, viewer, post)

	if evicted := s.recheckPostRooms(context.Background(), grants, user, t0.Add(5*time.Minute), postRoomRecheckBatch); len(evicted) != 0 {
		t.Fatalf("one undecided re-check mass-evicted: %+v", evicted)
	}
	if !grants.held(post) {
		t.Fatal("one blip dropped the grant")
	}
	evicted := s.recheckPostRooms(context.Background(), grants, user, t0.Add(10*time.Minute), postRoomRecheckBatch)
	if len(evicted) != 1 || evicted[0].PostID != post || evicted[0].Reason != "unresolved" {
		t.Fatalf("two consecutive undecided re-checks kept the seat open: %+v", evicted)
	}
	if grants.held(post) {
		t.Fatal("the cached grant outlived the fail-closed eviction")
	}
}

func TestPostRoomRecheckYesResetsUndecidedStreak(t *testing.T) {
	post := uuid.NewString()
	// join yes, then blip, yes, blip: never two in a row.
	answers := []error{nil, errors.New("blip"), nil, errors.New("blip")}
	viewer := &scriptedViewer{decide: func(_ string, call int) (bool, error) {
		err := answers[call-1]
		return err == nil, err
	}}
	s, grants, user, t0 := joinedServer(t, viewer, post)
	for i := 1; i <= 3; i++ {
		if evicted := s.recheckPostRooms(context.Background(), grants, user, t0.Add(time.Duration(i)*5*time.Minute), postRoomRecheckBatch); len(evicted) != 0 {
			t.Fatalf("sweep %d evicted although the blips were not consecutive: %+v", i, evicted)
		}
	}
	if !grants.held(post) {
		t.Fatal("a yes between two blips did not reset the streak")
	}
}

func TestPostRoomRecheckClearsCacheSoResubscribeReasks(t *testing.T) {
	post := uuid.NewString()
	viewer := &scriptedViewer{decide: func(_ string, call int) (bool, error) { return call != 2, nil }}
	s, grants, user, t0 := joinedServer(t, viewer, post)

	s.recheckPostRooms(context.Background(), grants, user, t0.Add(time.Minute), postRoomRecheckBatch)
	// Inside the original cache window: a re-subscribe must re-ask, not
	// reuse the join-time yes.
	s.mayJoinPostRoom(context.Background(), grants, user, post, t0.Add(2*time.Minute))
	if viewer.calls != 3 {
		t.Fatalf("a re-subscribe after eviction reused the cached yes (%d calls)", viewer.calls)
	}
}

func TestPostRoomRecheckWithoutAuthorityEvicts(t *testing.T) {
	post := uuid.NewString()
	s, grants, user, t0 := joinedServer(t, &scriptedViewer{decide: func(string, int) (bool, error) { return true, nil }}, post)
	s.opts.PostViewer = nil
	evicted := s.recheckPostRooms(context.Background(), grants, user, t0.Add(time.Minute), postRoomRecheckBatch)
	if len(evicted) != 1 || evicted[0].Reason != "unresolved" {
		t.Fatalf("a seat with no authority left to re-check it was kept: %+v", evicted)
	}
}

type fakeUnsubscriber struct{ channels []string }

func (f *fakeUnsubscriber) Unsubscribe(_ context.Context, channels ...string) error {
	f.channels = append(f.channels, channels...)
	return nil
}

func TestPostRoomEvictionLeavesRoomAndTellsClientLikeConversationRooms(t *testing.T) {
	post := uuid.NewString()
	s := &Server{opts: ServerOptions{EnablePostRooms: true}}
	pubsub := &fakeUnsubscriber{}
	outbound := make(chan []byte, 4)

	s.evictPostRooms(context.Background(), pubsub, outbound, uuid.New(), []postRoomEviction{{PostID: post, Reason: "denied"}})

	if len(pubsub.channels) != 1 || pubsub.channels[0] != "post:"+post {
		t.Fatalf("eviction did not leave post:%s (left %v)", post, pubsub.channels)
	}
	select {
	case frame := <-outbound:
		var m map[string]any
		if err := json.Unmarshal(frame, &m); err != nil {
			t.Fatal(err)
		}
		// Same control frame conversation rooms use ({"type":
		// "subscription_revoked","conversation_id":...}): the type, one id
		// key for the room, nothing else.
		if m["type"] != "subscription_revoked" || m["post_id"] != post || len(m) != 2 {
			t.Fatalf("frame %s does not match the conversation-room revocation shape", frame)
		}
	default:
		t.Fatal("the client was not told its post room was revoked")
	}
}

func TestPostRoomEvictionDoesNotBlockOnAFullClient(t *testing.T) {
	s := &Server{}
	outbound := make(chan []byte) // unbuffered, no reader
	done := make(chan struct{})
	go func() {
		s.evictPostRooms(context.Background(), &fakeUnsubscriber{}, outbound, uuid.New(), []postRoomEviction{{PostID: uuid.NewString(), Reason: "denied"}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("eviction blocked the reconcile loop on a slow client")
	}
}

func TestPostRoomRecheckIsBoundedPerSweepAndRoundRobins(t *testing.T) {
	posts := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	viewer := &scriptedViewer{decide: func(string, int) (bool, error) { return true, nil }}
	s, grants, user, t0 := joinedServer(t, viewer, posts...)
	joinCalls := viewer.calls

	const limit = 2
	seen := map[string]int{}
	for sweep := 1; sweep <= 3; sweep++ {
		before := viewer.calls
		s.recheckPostRooms(context.Background(), grants, user, t0.Add(time.Duration(sweep)*5*time.Minute), limit)
		if got := viewer.calls - before; got != limit {
			t.Fatalf("sweep %d re-asked %d posts; the bound is %d", sweep, got, limit)
		}
		for _, id := range viewer.asked[before:] {
			seen[id]++
		}
	}
	// Three sweeps of two cover five rooms once each and wrap onto one.
	if len(seen) != len(posts) {
		t.Fatalf("round-robin starved a room: %d of %d re-checked after 3 sweeps", len(seen), len(posts))
	}
	wrapped := 0
	for _, n := range seen {
		if n == 2 {
			wrapped++
		}
	}
	if wrapped != 1 || viewer.calls-joinCalls != 3*limit {
		t.Fatalf("expected exactly one room re-checked twice on wrap-around, got %v", seen)
	}
}

func TestPostRoomRecheckBatchIsEmptyWithoutRooms(t *testing.T) {
	grants := newPostRoomGrants(postRoomGrantTTL)
	if got := grants.nextBatch(postRoomRecheckBatch); got != nil {
		t.Fatalf("an empty connection produced a batch: %v", got)
	}
	grants.grant("a", time.Now())
	if got := grants.nextBatch(0); got != nil {
		t.Fatalf("a zero bound produced a batch: %v", got)
	}
}
