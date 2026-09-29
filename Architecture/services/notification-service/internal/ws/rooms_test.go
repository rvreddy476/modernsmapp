package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// Room re-authorization (P-8 e, 2026-09-29). Rooms used to be authorized at
// join only; every case here is a member the join let in who must not keep
// receiving frames once the authority stops saying yes.

type fakeRoomAuthz struct {
	mu      sync.Mutex
	answers map[string]bool // userID|room -> may stay
	err     error
	asked   []string
}

func (f *fakeRoomAuthz) MayStay(_ context.Context, userID, room string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, userID+"|"+room)
	if f.err != nil {
		return false, f.err
	}
	return f.answers[userID+"|"+room], nil
}

func newTestRooms(authz RoomAuthorizer) *RoomManager {
	return NewRoomManager(nil, "test-instance").WithAuthorizer(authz)
}

func dropNotice(t *testing.T, ch chan []byte) (room, reason string) {
	t.Helper()
	select {
	case raw := <-ch:
		var ev struct {
			Type  string            `json:"type"`
			Room  string            `json:"room"`
			Event string            `json:"event"`
			Data  map[string]string `json:"data"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("frame is not a RoomEvent: %s", raw)
		}
		if ev.Type != "unsubscribed" || ev.Event != "reauthorization" {
			t.Fatalf("unexpected frame %s", raw)
		}
		return ev.Room, ev.Data["reason"]
	default:
		t.Fatal("no unsubscribed frame reached the socket")
		return "", ""
	}
}

func TestReauthorizationDropsAMemberWhoNoLongerPasses(t *testing.T) {
	authz := &fakeRoomAuthz{answers: map[string]bool{"u1|post:p1": true, "u2|post:p1": false}}
	rm := newTestRooms(authz)
	keep, drop := make(chan []byte, 4), make(chan []byte, 4)
	rm.SubscribeUser("c1", "u1", "post:p1", keep)
	rm.SubscribeUser("c2", "u2", "post:p1", drop)

	if n := rm.ReauthorizeOnce(context.Background()); n != 1 {
		t.Fatalf("dropped %d, want 1", n)
	}
	if rm.RoomCount("post:p1") != 1 {
		t.Fatalf("room has %d members, want the one who still passes", rm.RoomCount("post:p1"))
	}
	room, reason := dropNotice(t, drop)
	if room != "post:p1" || reason != "no_longer_authorized" {
		t.Fatalf("notice %s %s", room, reason)
	}
	select {
	case raw := <-keep:
		t.Fatalf("the member who still passes got a frame: %s", raw)
	default:
	}
	// A frame after the sweep reaches the survivor only.
	rm.BroadcastToRoom("post:p1", []byte(`{"type":"room_event"}`))
	if len(keep) != 1 || len(drop) != 0 {
		t.Fatalf("broadcast reached keep=%d drop=%d", len(keep), len(drop))
	}
}

// An authority that cannot answer is not a reason to keep delivering.
func TestReauthorizationFailsClosedOnAnError(t *testing.T) {
	authz := &fakeRoomAuthz{answers: map[string]bool{"u1|post:p1": true}, err: errors.New("post-service 503")}
	rm := newTestRooms(authz)
	ch := make(chan []byte, 4)
	rm.SubscribeUser("c1", "u1", "post:p1", ch)
	if n := rm.ReauthorizeOnce(context.Background()); n != 1 {
		t.Fatalf("dropped %d, want 1", n)
	}
	if _, reason := dropNotice(t, ch); reason != "unresolved" {
		t.Fatalf("reason %q", reason)
	}
	if rm.RoomCount("post:p1") != 0 {
		t.Fatal("member survived an unresolved re-check")
	}
}

// A seat with no identity cannot be re-asked and is dropped, so the legacy
// Subscribe cannot become a way to sit in a room without being re-checked.
func TestReauthorizationDropsSeatsWithoutIdentity(t *testing.T) {
	rm := newTestRooms(&fakeRoomAuthz{answers: map[string]bool{}})
	ch := make(chan []byte, 4)
	rm.Subscribe("c1", "post:p1", ch)
	if n := rm.ReauthorizeOnce(context.Background()); n != 1 {
		t.Fatalf("dropped %d, want 1", n)
	}
	if _, reason := dropNotice(t, ch); reason != "no_identity" {
		t.Fatalf("reason %q", reason)
	}
}

// Every seat is asked of the authority, once per sweep, as the user who
// holds it; a member who still passes keeps every room.
func TestReauthorizationAsksForEverySeat(t *testing.T) {
	authz := &fakeRoomAuthz{answers: map[string]bool{"u1|post:p1": true, "u1|poll:p1": true, "u1|notifications:u1": true}}
	rm := newTestRooms(authz)
	ch := make(chan []byte, 4)
	for _, room := range []string{"post:p1", "poll:p1", "notifications:u1"} {
		rm.SubscribeUser("c1", "u1", room, ch)
	}
	if n := rm.ReauthorizeOnce(context.Background()); n != 0 {
		t.Fatalf("dropped %d, want 0", n)
	}
	if len(authz.asked) != 3 {
		t.Fatalf("asked %v, want every seat once", authz.asked)
	}
	for _, room := range []string{"post:p1", "poll:p1", "notifications:u1"} {
		if rm.RoomCount(room) != 1 {
			t.Fatalf("%s lost its member", room)
		}
	}
}

// A re-subscribe that replaced the seat between the snapshot and the drop is
// a fresh, join-authorized membership and is left alone.
func TestReauthorizationDoesNotDropAReplacedSeat(t *testing.T) {
	authz := &fakeRoomAuthz{answers: map[string]bool{"u2|post:p1": false}}
	rm := newTestRooms(authz)
	old, fresh := make(chan []byte, 4), make(chan []byte, 4)
	rm.SubscribeUser("c1", "u2", "post:p1", old)
	// Swap the seat under the sweep: the snapshot holds `old`, the room
	// holds `fresh` by the time the drop is applied.
	swapped := &swappingAuthz{inner: authz, swap: func() { rm.SubscribeUser("c1", "u2", "post:p1", fresh) }}
	rm.authz = swapped
	if n := rm.ReauthorizeOnce(context.Background()); n != 0 {
		t.Fatalf("dropped %d, want 0 (the seat was replaced)", n)
	}
	if rm.RoomCount("post:p1") != 1 {
		t.Fatal("the replacement seat was dropped")
	}
}

type swappingAuthz struct {
	inner RoomAuthorizer
	swap  func()
	once  sync.Once
}

func (s *swappingAuthz) MayStay(ctx context.Context, userID, room string) (bool, error) {
	s.once.Do(s.swap)
	return s.inner.MayStay(ctx, userID, room)
}

// Without an authorizer the sweep is a no-op: the join-time decision stands
// (the pre-2026-09-29 behaviour), and the ticker loop returns at once.
func TestReauthorizationWithoutAnAuthorizerIsANoOp(t *testing.T) {
	rm := NewRoomManager(nil, "test-instance")
	rm.SubscribeUser("c1", "u1", "post:p1", make(chan []byte, 1))
	if n := rm.ReauthorizeOnce(context.Background()); n != 0 || rm.RoomCount("post:p1") != 1 {
		t.Fatalf("no-op sweep dropped %d", n)
	}
	done := make(chan struct{})
	go func() { rm.StartReauthorizer(context.Background(), time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartReauthorizer without an authorizer did not return")
	}
}

// The periodic loop runs the sweep on its interval and stops with the context.
func TestReauthorizerRunsPeriodically(t *testing.T) {
	authz := &fakeRoomAuthz{answers: map[string]bool{"u1|post:p1": false}}
	rm := newTestRooms(authz)
	ch := make(chan []byte, 4)
	rm.SubscribeUser("c1", "u1", "post:p1", ch)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rm.StartReauthorizer(ctx, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for rm.RoomCount("post:p1") != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if rm.RoomCount("post:p1") != 0 {
		t.Fatal("the periodic sweep never dropped the member")
	}
	if _, reason := dropNotice(t, ch); reason != "no_longer_authorized" {
		t.Fatalf("reason %q", reason)
	}
}

func TestUnsubscribeAllAndBroadcastStillWork(t *testing.T) {
	rm := newTestRooms(&fakeRoomAuthz{answers: map[string]bool{}})
	ch := make(chan []byte, 1)
	rm.SubscribeUser("c1", "u1", "post:p1", ch)
	rm.SubscribeUser("c1", "u1", "poll:p1", ch)
	rm.BroadcastToRoom("post:p1", []byte("x"))
	if len(ch) != 1 {
		t.Fatal("broadcast lost")
	}
	rm.UnsubscribeAll("c1")
	if rm.RoomCount("post:p1") != 0 || rm.RoomCount("poll:p1") != 0 {
		t.Fatal("UnsubscribeAll left a seat")
	}
}
