package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// fakeRoomAuth answers from a set of allowed user|room pairs.
type fakeRoomAuth struct {
	mu      sync.Mutex
	allowed map[string]bool
	err     error
	calls   int
	bearers []string
}

func (f *fakeRoomAuth) allow(user uuid.UUID, room string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allowed == nil {
		f.allowed = map[string]bool{}
	}
	f.allowed[user.String()+"|"+room] = ok
}

func (f *fakeRoomAuth) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeRoomAuth) MayJoin(_ context.Context, bearer string, user uuid.UUID, room string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.bearers = append(f.bearers, bearer)
	if f.err != nil {
		return false, f.err
	}
	return f.allowed[user.String()+"|"+room], nil
}

func TestMayUseScopedRoomRules(t *testing.T) {
	user, call := uuid.New(), uuid.NewString()
	auth := &fakeRoomAuth{}
	auth.allow(user, call, true)
	now := time.Now()

	off := &Server{opts: ServerOptions{EnableScopedRooms: false, CallRoomAuthorizer: auth}}
	if _, ok := off.mayUseScopedRoom(context.Background(), newPostRoomGrants(time.Minute), scopedRoomCall, user, "b", call, now); ok {
		t.Fatal("a scoped room was admitted with the flag off")
	}

	noAuth := &Server{opts: ServerOptions{EnableScopedRooms: true}}
	for _, kind := range []string{scopedRoomCall, scopedRoomUpdate, scopedRoomGroupPost} {
		if _, ok := noAuth.mayUseScopedRoom(context.Background(), newPostRoomGrants(time.Minute), kind, user, "b", call, now); ok {
			t.Fatalf("%s room admitted with no authorizer wired", kind)
		}
	}

	on := &Server{opts: ServerOptions{EnableScopedRooms: true, CallRoomAuthorizer: auth}}
	grants := newPostRoomGrants(time.Minute)
	if _, ok := on.mayUseScopedRoom(context.Background(), grants, scopedRoomCall, user, "b", "not-a-uuid", now); ok {
		t.Fatal("a non-UUID room id was admitted")
	}
	if _, ok := on.mayUseScopedRoom(context.Background(), grants, scopedRoomCall, uuid.New(), "b", call, now); ok {
		t.Fatal("a user the owner refused was admitted")
	}
	channel, ok := on.mayUseScopedRoom(context.Background(), grants, scopedRoomCall, user, "bearer-1", strings.ToUpper(call), now)
	if !ok || channel != "call:"+call {
		t.Fatalf("participant refused or channel not canonical: %q ok=%v", channel, ok)
	}
	before := auth.calls
	if _, ok := on.mayUseScopedRoom(context.Background(), grants, scopedRoomCall, user, "bearer-1", call, now.Add(time.Second)); !ok || auth.calls != before {
		t.Fatalf("a fresh grant re-asked the owner (calls %d -> %d)", before, auth.calls)
	}
	if auth.bearers[len(auth.bearers)-1] != "bearer-1" {
		t.Fatal("the connection's bearer was not forwarded")
	}

	broken := &fakeRoomAuth{err: errors.New("down")}
	on.opts.CallRoomAuthorizer = broken
	if _, ok := on.mayUseScopedRoom(context.Background(), newPostRoomGrants(time.Minute), scopedRoomCall, user, "b", call, now); ok {
		t.Fatal("an undecided owner admitted the room")
	}
}

func TestRecheckScopedRoomsEvicts(t *testing.T) {
	user := uuid.New()
	keep, deny, flaky := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := &fakeRoomAuth{}
	auth.allow(user, keep, true)
	s := &Server{opts: ServerOptions{EnableScopedRooms: true, CallRoomAuthorizer: auth}}
	grants := newPostRoomGrants(time.Minute)
	now := time.Now()
	for _, id := range []string{keep, deny, flaky} {
		grants.grant("call:"+id, now)
	}
	grants.grant("update:"+uuid.NewString(), now) // no update authorizer wired

	evicted := s.recheckScopedRooms(context.Background(), grants, user, "b", now, 100)
	got := map[string]bool{}
	for _, ch := range evicted {
		got[ch] = true
	}
	if !got["call:"+deny] || !got["call:"+flaky] || got["call:"+keep] || len(got) != 3 {
		t.Fatalf("evicted %v; want the refused call rooms and the unwired update room", evicted)
	}
	if !grants.held("call:" + keep) {
		t.Fatal("the still-allowed room lost its grant")
	}

	// An owner outage keeps a seat once, then evicts (fail closed).
	auth.setErr(errors.New("down"))
	if ev := s.recheckScopedRooms(context.Background(), grants, user, "b", now, 100); len(ev) != 0 {
		t.Fatalf("first undecided sweep evicted %v", ev)
	}
	if ev := s.recheckScopedRooms(context.Background(), grants, user, "b", now, 100); len(ev) != 1 || ev[0] != "call:"+keep {
		t.Fatalf("second undecided sweep evicted %v, want the held room", ev)
	}
}

func callServiceStub(t *testing.T, status int, body string) (*HTTPCallRoomAuthorizer, *string) {
	t.Helper()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if !strings.HasPrefix(r.URL.Path, "/v1/calls/") || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewHTTPCallRoomAuthorizer(srv.URL, srv.Client()), &gotAuth
}

func callBody(state string, user uuid.UUID, joinState string) string {
	return fmt.Sprintf(`{"data":{"state":%q,"participants":[{"user_id":%q,"join_state":"joined"},{"user_id":%q,"join_state":%q}]}}`,
		state, uuid.NewString(), user, joinState)
}

func TestHTTPCallRoomAuthorizer(t *testing.T) {
	user, call := uuid.New(), uuid.NewString()
	cases := []struct {
		name    string
		status  int
		body    string
		want    bool
		wantErr bool
	}{
		{"joined participant, active call", 200, callBody("active", user, "joined"), true, false},
		{"invited participant, ringing call", 200, callBody("ringing", user, "not_joined"), true, false},
		{"left the call", 200, callBody("active", user, "left"), false, false},
		{"removed from the call", 200, callBody("active", user, "removed"), false, false},
		{"ended call", 200, callBody("ended", user, "joined"), false, false},
		{"not listed", 200, callBody("active", uuid.New(), "joined"), false, false},
		{"not a participant (403)", 403, `{"error":{}}`, false, false},
		{"unknown call (404)", 404, `{"error":{}}`, false, false},
		{"calls disabled (503)", 503, `{"data":{"state":"active"}}`, false, true},
		{"garbage", 200, `not json`, false, true},
	}
	for _, tc := range cases {
		auth, gotAuth := callServiceStub(t, tc.status, tc.body)
		ok, err := auth.MayJoin(context.Background(), "user-bearer", user, call)
		if ok != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("%s: ok=%v err=%v, want ok=%v err=%v", tc.name, ok, err, tc.want, tc.wantErr)
		}
		if *gotAuth != "Bearer user-bearer" {
			t.Errorf("%s: call-service saw Authorization %q", tc.name, *gotAuth)
		}
	}
	if ok, err := NewHTTPCallRoomAuthorizer("", nil).MayJoin(context.Background(), "b", user, call); ok || err == nil {
		t.Fatal("an unconfigured authority must refuse with an error")
	}
	auth, _ := callServiceStub(t, 200, callBody("active", user, "joined"))
	if ok, err := auth.MayJoin(context.Background(), "", user, call); ok || err == nil {
		t.Fatal("a check without a bearer must refuse")
	}
}

// Audit L2 end to end, with EnableScopedRooms on: a socket the call's owner
// does not vouch for can neither hear the call room nor publish into it; a
// participant can do both.
func TestWSScopedCallRoomRequiresOwnerCheck(t *testing.T) {
	auth := &fakeRoomAuth{}
	h := newLiveHarness(t, func(o *ServerOptions) {
		o.EnableScopedRooms = true
		o.CallRoomAuthorizer = auth
	})
	member, outsider, call := uuid.New(), uuid.New(), uuid.NewString()
	auth.allow(member, call, true)
	ctx := context.Background()

	// Outsider: subscribe is refused (no seat), publish is refused.
	out := h.dial(outsider)
	h.send(out, map[string]any{"type": "subscribe_call", "call_id": call})
	spy := h.rdb.Subscribe(ctx, "call:"+call)
	t.Cleanup(func() { _ = spy.Close() })
	if _, err := spy.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	h.send(out, map[string]any{"type": "call_join", "call_id": call})
	h.send(out, map[string]any{"type": "subscribe_update", "update_id": uuid.NewString()})
	h.send(out, map[string]any{"type": "subscribe_group_post", "post_id": uuid.NewString()})

	// Member: subscribes and publishes.
	in := h.dial(member)
	h.send(in, map[string]any{"type": "subscribe_call", "call_id": call})
	h.waitSubscribers("call:"+call, 2) // the spy + the member, never the outsider
	h.send(in, map[string]any{"type": "call_join", "call_id": call})

	msg := receiveWithin(t, spy, time.Second)
	if !strings.Contains(msg, member.String()) || strings.Contains(msg, outsider.String()) {
		t.Fatalf("first frame on the call room = %s; want the member's call_join only", msg)
	}
	if got := readFrame(t, in); !strings.Contains(got, `"call_join"`) {
		t.Fatalf("member did not hear the room: %s", got)
	}
	// The outsider's socket hears nothing from the room.
	marker := `{"type":"message","payload":{"marker":"` + uuid.NewString() + `"}}`
	h.publish("chat:"+outsider.String(), marker)
	if got := readFrame(t, out); got != marker {
		t.Fatalf("an outsider received a call room frame: %s", got)
	}
}

func receiveWithin(t *testing.T, sub *redis.PubSub, d time.Duration) string {
	t.Helper()
	select {
	case m := <-sub.Channel():
		return m.Payload
	case <-time.After(d):
		t.Fatal("nothing published to the room")
		return ""
	}
}
