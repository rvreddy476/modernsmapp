package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// The in-memory store (internal/storetest) under the names the older tests
// use.
func newMemStore() *storetest.MemStore  { return storetest.New() }
func newFakeStore() *storetest.MemStore { return storetest.New() }

var _ Store = (*storetest.MemStore)(nil)

// fakeLiveKit (live_test.go) gains the room-admin calls as no-ops.
func (fakeLiveKit) DeleteRoom(context.Context, string) error                { return nil }
func (fakeLiveKit) RemoveParticipant(context.Context, string, string) error { return nil }
func (fakeLiveKit) ListParticipants(context.Context, string) ([]livekit.Participant, error) {
	return nil, livekit.ErrRoomNotFound
}

// recLiveKit records the room-admin calls and answers ListParticipants
// from a per-room table (nil entry = room not found, listErr = LiveKit down).
type recLiveKit struct {
	fakeLiveKit
	mu           sync.Mutex
	deleted      []string
	removed      []string
	egressStarts int
	egressStops  []string
	rooms        map[string][]livekit.Participant
	listErr      error
}

func newRecLiveKit() *recLiveKit { return &recLiveKit{rooms: map[string][]livekit.Participant{}} }

func (r *recLiveKit) DeleteRoom(_ context.Context, room string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, room)
	return nil
}

func (r *recLiveKit) RemoveParticipant(_ context.Context, room, identity string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, room+"/"+identity)
	return nil
}

func (r *recLiveKit) ListParticipants(_ context.Context, room string) ([]livekit.Participant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listErr != nil {
		return nil, r.listErr
	}
	p, ok := r.rooms[room]
	if !ok {
		return nil, livekit.ErrRoomNotFound
	}
	return p, nil
}

func (r *recLiveKit) StartEgressToS3(context.Context, string, string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.egressStarts++
	return "EG_1", nil
}

func (r *recLiveKit) StopEgress(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.egressStops = append(r.egressStops, id)
	return nil
}

var _ livekit.Client = (*recLiveKit)(nil)

// listingStore (listing_test.go) has no moderators.
func (f *listingStore) ModeratorsFor(context.Context, []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return map[uuid.UUID][]uuid.UUID{}, nil
}

// recEvents records published room events; allowAll disables throttling.
type recEvents struct {
	mu       sync.Mutex
	events   []map[string]any
	allowed  map[string]time.Time
	now      func() time.Time
	allowAll bool
}

func newRecEvents(now func() time.Time) *recEvents {
	return &recEvents{allowed: map[string]time.Time{}, now: now}
}

func (r *recEvents) Publish(_ context.Context, _ uuid.UUID, body []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	r.events = append(r.events, m)
	return nil
}

func (r *recEvents) Allow(_ context.Context, key string, window time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.allowAll {
		return true
	}
	now := r.now()
	if until, ok := r.allowed[key]; ok && now.Before(until) {
		return false
	}
	r.allowed[key] = now.Add(window)
	return true
}

func (r *recEvents) ofType(t string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, e := range r.events {
		if e["type"] == t {
			out = append(out, e)
		}
	}
	return out
}

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// rig is a service over the memory store, a recording LiveKit, recorded room
// events and a fake clock shared by all three.
type rig struct {
	svc   *Service
	store *storetest.MemStore
	lk    *recLiveKit
	ev    *recEvents
	clock *fakeClock
	graph *fakeGraph
}

func newRig(pilot ...uuid.UUID) *rig {
	clock := newFakeClock()
	store := storetest.New()
	store.Now = clock.Now
	lk := newRecLiveKit()
	graph := &fakeGraph{follows: map[string]bool{}, blocked: map[string]bool{}}
	svc := New(store, lk, graph, nil, Config{PilotUserIDs: pilot})
	ev := newRecEvents(clock.Now)
	svc.rt = ev
	return &rig{svc: svc, store: store, lk: lk, ev: ev, clock: clock, graph: graph}
}
