package http

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

type fakeViewer struct {
	answer bool
	err    error
	calls  int
	asked  []string
}

func (f *fakeViewer) MayView(_ context.Context, viewerID, postID string) (bool, error) {
	f.calls++
	f.asked = append(f.asked, viewerID+"→"+postID)
	return f.answer, f.err
}

func TestPostRoomAdmitsOnlyTheAuthoritysYes(t *testing.T) {
	user := uuid.New()
	post := uuid.New()
	now := time.Now()

	yes := &fakeViewer{answer: true}
	s := &Server{opts: ServerOptions{EnablePostRooms: true, PostViewer: yes}}
	id, ok := s.mayJoinPostRoom(context.Background(), newPostRoomGrants(time.Minute), user, post.String(), now)
	if !ok || id != post.String() {
		t.Fatalf("a viewer the authority admits was refused (ok=%v id=%s)", ok, id)
	}
	if yes.asked[0] != user.String()+"→"+post.String() {
		t.Fatalf("asked %q", yes.asked[0])
	}

	no := &fakeViewer{answer: false}
	s = &Server{opts: ServerOptions{EnablePostRooms: true, PostViewer: no}}
	if _, ok := s.mayJoinPostRoom(context.Background(), newPostRoomGrants(time.Minute), user, post.String(), now); ok {
		t.Fatal("a viewer the authority refuses joined the room")
	}

	broken := &fakeViewer{err: errors.New("down")}
	s = &Server{opts: ServerOptions{EnablePostRooms: true, PostViewer: broken}}
	if _, ok := s.mayJoinPostRoom(context.Background(), newPostRoomGrants(time.Minute), user, post.String(), now); ok {
		t.Fatal("an undecided authority admitted a viewer — must fail closed")
	}
}

func TestPostRoomRefusedWhenDisabledOrMalformed(t *testing.T) {
	user := uuid.New()
	yes := &fakeViewer{answer: true}
	off := &Server{opts: ServerOptions{EnablePostRooms: false, PostViewer: yes}}
	if _, ok := off.mayJoinPostRoom(context.Background(), newPostRoomGrants(time.Minute), user, uuid.NewString(), time.Now()); ok {
		t.Fatal("post rooms joined while disabled")
	}
	noViewer := &Server{opts: ServerOptions{EnablePostRooms: true}}
	if _, ok := noViewer.mayJoinPostRoom(context.Background(), newPostRoomGrants(time.Minute), user, uuid.NewString(), time.Now()); ok {
		t.Fatal("post rooms joined with no authority configured")
	}
	on := &Server{opts: ServerOptions{EnablePostRooms: true, PostViewer: yes}}
	if _, ok := on.mayJoinPostRoom(context.Background(), newPostRoomGrants(time.Minute), user, "../chat:someone", time.Now()); ok {
		t.Fatal("a non-uuid room name was accepted — the room name must be derived from a parsed id")
	}
	if yes.calls != 0 {
		t.Fatal("the authority was asked about a malformed id")
	}
}

func TestPostRoomGrantIsCachedPerConnectionAndExpires(t *testing.T) {
	user, post := uuid.New(), uuid.New()
	viewer := &fakeViewer{answer: true}
	s := &Server{opts: ServerOptions{EnablePostRooms: true, PostViewer: viewer}}
	grants := newPostRoomGrants(time.Minute)
	t0 := time.Now()
	s.mayJoinPostRoom(context.Background(), grants, user, post.String(), t0)
	s.mayJoinPostRoom(context.Background(), grants, user, post.String(), t0.Add(30*time.Second))
	if viewer.calls != 1 {
		t.Fatalf("a re-subscribe inside the window re-asked the authority (%d calls)", viewer.calls)
	}
	s.mayJoinPostRoom(context.Background(), grants, user, post.String(), t0.Add(2*time.Minute))
	if viewer.calls != 2 {
		t.Fatalf("an expired grant was reused (%d calls)", viewer.calls)
	}
	grants.revoke(post.String())
	s.mayJoinPostRoom(context.Background(), grants, user, post.String(), t0.Add(2*time.Minute+time.Second))
	if viewer.calls != 3 {
		t.Fatal("unsubscribe did not drop the grant")
	}
	// A grant belongs to one connection; a fresh connection starts empty.
	other := newPostRoomGrants(time.Minute)
	s.mayJoinPostRoom(context.Background(), other, user, post.String(), t0)
	if viewer.calls != 4 {
		t.Fatal("a grant leaked across connections")
	}
}

func TestBetaGateAdmitsPostRoomsOnlyWhenOwnerChecked(t *testing.T) {
	off := &Server{opts: ServerOptions{EnableScopedRooms: false, EnablePostRooms: false}}
	if !off.betaRoomGateRejects("subscribe_post") {
		t.Fatal("subscribe_post passed the gate with post rooms disabled")
	}
	on := &Server{opts: ServerOptions{EnableScopedRooms: false, EnablePostRooms: true}}
	for _, f := range []string{"subscribe_post", "unsubscribe_post"} {
		if on.betaRoomGateRejects(f) {
			t.Errorf("%s gated although post rooms are owner-checked", f)
		}
	}
	// Post rooms do not open any other client-selected room.
	for _, f := range []string{"subscribe_group_post", "group_post_typing", "subscribe_call", "subscribe_update", "conversation.enter"} {
		if !on.betaRoomGateRejects(f) {
			t.Errorf("%s slipped through the gate on the post-rooms flag", f)
		}
	}
}

func TestHTTPPostViewAuthorizerAsksPostService(t *testing.T) {
	var gotPath, gotQuery, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotKey = r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Internal-Service-Key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"visible": true})
	}))
	defer srv.Close()
	a := NewHTTPPostViewAuthorizer(srv.URL, "k", srv.Client())
	ok, err := a.MayView(context.Background(), "viewer-1", "post-1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if gotPath != "/v1/internal/posts/post-1/visibility" || gotQuery != "viewer_id=viewer-1" || gotKey != "k" {
		t.Fatalf("asked %s?%s key=%q", gotPath, gotQuery, gotKey)
	}

	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer deny.Close()
	if ok, err := NewHTTPPostViewAuthorizer(deny.URL, "k", deny.Client()).MayView(context.Background(), "v", "p"); ok || err == nil {
		t.Fatal("a non-200 must be an error (undecided), never a yes")
	}
}

func TestGroupPostTypingCarriesNoIdentity(t *testing.T) {
	frame := groupPostTypingFrame("post-1")
	var m map[string]any
	if err := json.Unmarshal(frame, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "group_post_typing" || m["post_id"] != "post-1" {
		t.Fatalf("frame %s", frame)
	}
	for _, forbidden := range []string{"user_id", "sender_id", "actor_id", "author_id"} {
		if _, present := m[forbidden]; present {
			t.Fatalf("typing frame names the sender via %q — that unmasks an anonymous author", forbidden)
		}
	}
}
